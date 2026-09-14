package nexserver

import (
	"encoding/binary"
	"sync"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/ProtariumNetwork/fast-racing-neo/globals"
)

// * Whether NEX structures carry a version/length header is the single most
// * consequential wire-format decision this server makes. Get it wrong and
// * every structure the game sends or receives is misparsed - login fails, or
// * matchmaking silently returns garbage.
// *
// * It is also the one thing that cannot be settled from the outside, because
// * it is NOT decided by the NEX library version. It is decided by the PRUDP
// * minor version the console announces in its SYN/CONNECT handshake:
// * kinnay's client turns headers on when the negotiated minor version is 3 or
// * higher (nintendo/nex/rmc.py: `if self._client.minor_version() >= 3`), and
// * nex-go simply echoes back whatever minor version the client asked for
// * (prudp_endpoint.go: "No change needed, we can just support what the client
// * wants"). So the console picks, unilaterally, and the server has to agree.
// *
// * FAST Racing NEO links NEX 3.9.1, so production explicitly enables these
// * headers. Auto-detection remains available for diagnostics and captures.

const (
	// StructureHeaderAuto detects the setting from the first login.
	StructureHeaderAuto = "auto"
	// StructureHeaderOn forces headers on.
	StructureHeaderOn = "on"
	// StructureHeaderOff forces headers off.
	StructureHeaderOff = "off"
)

var (
	structureHeaderOnce   sync.Once
	structureHeaderMutex  sync.Mutex
	structureHeaderKnown  bool
	structureHeaderInUse  bool
	structureHeaderSource string
)

// applyStructureHeaderMode applies a forced setting, or leaves detection armed.
func applyStructureHeaderMode() {
	switch globals.Settings.StructureHeader {
	case StructureHeaderOn:
		setStructureHeader(true, "forced by FAST_STRUCTURE_HEADER=on")
	case StructureHeaderOff:
		setStructureHeader(false, "forced by FAST_STRUCTURE_HEADER=off")
	default:
		globals.Logger.Info("Structure headers: auto-detecting from the first login")
	}
}

// setStructureHeader applies the decision to both servers.
//
// Both must agree: the authentication server writes the Kerberos ticket and
// RVConnectionData, and the secure server parses every game structure. A split
// decision would break one of them.
func setStructureHeader(useHeader bool, reason string) {
	structureHeaderMutex.Lock()
	defer structureHeaderMutex.Unlock()

	structureHeaderKnown = true
	structureHeaderInUse = useHeader
	structureHeaderSource = reason

	if globals.AuthenticationServer != nil {
		globals.AuthenticationServer.ByteStreamSettings.UseStructureHeader = useHeader
	}

	if globals.SecureServer != nil {
		globals.SecureServer.ByteStreamSettings.UseStructureHeader = useHeader
	}

	state := "disabled"
	if useHeader {
		state = "enabled"
	}

	globals.Logger.Successf("Structure headers %s (%s)", state, reason)
}

// detectStructureHeader inspects the first login request and decides whether
// the console is using structure headers.
//
// It works off the DataHolder the console sends as login data. A DataHolder is
// laid out as:
//
//	String   type name  ("NintendoLoginData" or "AuthenticationInfo")
//	UInt32   length of everything that follows, including the next field
//	UInt32   length of the object data
//	bytes    object data
//
// The DataHolder framing itself never carries a structure header, so it parses
// identically either way - but the object inside it is a structure, so it is
// prefixed with a version byte and a UInt32 content length exactly when headers
// are in use. Checking whether that length field matches the bytes actually
// present is an unambiguous test: a five byte prefix whose embedded length
// equals the remaining size is not something the header-less layout produces by
// accident.
func detectStructureHeader(packet nex.PacketInterface) {
	if globals.Settings.StructureHeader != StructureHeaderAuto {
		return
	}

	structureHeaderMutex.Lock()
	decided := structureHeaderKnown
	structureHeaderMutex.Unlock()

	if decided {
		return
	}

	request := packet.RMCMessage()
	if request == nil || !request.IsRequest {
		return
	}

	objectData, ok := loginDataObject(request.ProtocolID, request.MethodID, request.Parameters)
	if !ok {
		return
	}

	useHeader, ok := objectHasStructureHeader(objectData)
	if !ok {
		globals.Logger.Warning("Could not determine structure header use from the first login; keeping the default")
		return
	}

	structureHeaderOnce.Do(func() {
		setStructureHeader(useHeader, "detected from the console's login data")
	})
}

// loginDataObject extracts the raw object bytes from the DataHolder carried by
// the login-style requests, returning false when the request is not one of them
// or is too malformed to read.
func loginDataObject(protocolID uint16, methodID uint32, parameters []byte) ([]byte, bool) {
	cursor := 0

	switch {
	case protocolID == 0x0A && methodID == 0x02:
		// * TicketGranting::LoginEx(String strUserName, DataHolder oExtraData)
		var ok bool
		if cursor, ok = skipString(parameters, cursor); !ok {
			return nil, false
		}

	case protocolID == 0x0B && methodID == 0x04:
		// * SecureConnection::RegisterEx(List<StationURL> vecMyURLs, DataHolder hCustomData)
		var ok bool
		if cursor, ok = skipStationURLList(parameters, cursor); !ok {
			return nil, false
		}

	default:
		return nil, false
	}

	// * DataHolder: type name, then two lengths, then the object data.
	cursor, ok := skipString(parameters, cursor)
	if !ok {
		return nil, false
	}

	if cursor+8 > len(parameters) {
		return nil, false
	}

	cursor += 4 // * outer length, which includes the inner length field

	objectLength := int(binary.LittleEndian.Uint32(parameters[cursor : cursor+4]))
	cursor += 4

	if objectLength < 0 || cursor+objectLength > len(parameters) {
		return nil, false
	}

	return parameters[cursor : cursor+objectLength], true
}

// objectHasStructureHeader reports whether a structure's bytes are framed with
// version/length headers.
//
// A header is a version byte plus a UInt32 content length, and crucially there
// is one per class in the type's inheritance chain, outermost first. So an
// AuthenticationInfo - which derives from Data - is framed twice: an empty
// header for Data, then one for AuthenticationInfo's own fields.
//
// The test is therefore to walk the chain and see whether it accounts for the
// object exactly. That is a strong signal: a header-less structure begins with
// its first real field, and interpreting that as a length almost always runs
// past the end of the buffer. The login structures happen to start with a
// String, whose two-byte length makes the would-be UInt32 absorb the first
// characters of the token and overshoot immediately.
func objectHasStructureHeader(object []byte) (bool, bool) {
	if len(object) == 0 {
		return false, false
	}

	const maxStructureVersion = 8

	cursor := 0
	chain := 0

	for cursor+5 <= len(object) {
		version := object[cursor]
		length := int(binary.LittleEndian.Uint32(object[cursor+1 : cursor+5]))

		if version > maxStructureVersion || length < 0 || cursor+5+length > len(object) {
			break
		}

		cursor += 5 + length
		chain++
	}

	if chain > 0 && cursor == len(object) {
		return true, true
	}

	// * Confirm the header-less reading rather than just assuming it: the
	// * object should start with a String whose length fits.
	if len(object) >= 2 {
		stringLength := int(binary.LittleEndian.Uint16(object[0:2]))
		if stringLength > 0 && 2+stringLength <= len(object) {
			return false, true
		}
	}

	return false, false
}

// skipString advances past a NEX String (UInt16 length, then that many bytes).
func skipString(data []byte, cursor int) (int, bool) {
	if cursor+2 > len(data) {
		return cursor, false
	}

	length := int(binary.LittleEndian.Uint16(data[cursor : cursor+2]))
	cursor += 2

	if cursor+length > len(data) {
		return cursor, false
	}

	return cursor + length, true
}

// skipStationURLList advances past a List<StationURL>, which is a UInt32 count
// followed by that many Strings.
func skipStationURLList(data []byte, cursor int) (int, bool) {
	if cursor+4 > len(data) {
		return cursor, false
	}

	count := int(binary.LittleEndian.Uint32(data[cursor : cursor+4]))
	cursor += 4

	// * A count this large means we are not looking at what we think we are.
	if count < 0 || count > 64 {
		return cursor, false
	}

	for i := 0; i < count; i++ {
		var ok bool
		if cursor, ok = skipString(data, cursor); !ok {
			return cursor, false
		}
	}

	return cursor, true
}

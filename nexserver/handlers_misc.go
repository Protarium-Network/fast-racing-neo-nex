package nexserver

import (
	"encoding/hex"
	"net"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/constants"
	"github.com/PretendoNetwork/nex-go/v2/types"
	nattraversal "github.com/PretendoNetwork/nex-protocols-go/v2/nat-traversal"
	ticketgranting "github.com/PretendoNetwork/nex-protocols-go/v2/ticket-granting"
	"github.com/ProtariumNetwork/fast-racing-neo/globals"
)

// observedAddress returns the address a connection's packets actually arrive
// from. This is the ground truth for NAT traversal: whatever a console believes
// its external address to be, this is where replies have to go.
func observedAddress(connection *nex.PRUDPConnection) (string, uint16) {
	switch address := connection.Address().(type) {
	case *net.UDPAddr:
		return address.IP.String(), uint16(address.Port)
	case *net.TCPAddr:
		return address.IP.String(), uint16(address.Port)
	default:
		return "", 0
	}
}

// * ---------------------------------------------------------------------------
// * NAT Traversal
// * ---------------------------------------------------------------------------

// requestProbeInitiation asks a set of peers to send a probe packet at the
// caller, so the caller's NAT opens a path for their traffic.
//
// common-go implements only the later RequestProbeInitiationExt. This is the
// original form, which a NEX 3.0.1 title may well use instead: it takes station
// URLs directly rather than strings, and the station to probe is implied to be
// the caller's own.
//
// Response: no return values.
func requestProbeInitiation(err error, packet nex.PacketInterface, callID uint32, urlTargetList types.List[types.StationURL]) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Core.InvalidArgument, err.Error())
	}

	connection := packet.Sender().(*nex.PRUDPConnection)
	endpoint := connection.Endpoint().(*nex.PRUDPEndPoint)
	server := endpoint.Server

	// * The station each target should probe is the caller's public station.
	stationToProbe := types.NewStationURL("")
	for _, stationURL := range connection.StationURLs {
		if stationURL.IsPublic() {
			stationToProbe = stationURL.Copy().(types.StationURL)
			break
		}
	}

	requestStream := nex.NewByteStreamOut(endpoint.LibraryVersions(), endpoint.ByteStreamSettings())
	stationToProbe.WriteTo(requestStream)

	request := nex.NewRMCRequest(endpoint)
	request.ProtocolID = nattraversal.ProtocolID
	request.MethodID = nattraversal.MethodInitiateProbe
	request.CallID = 0xFFFF0000 + callID
	request.Parameters = requestStream.Bytes()

	requestBytes := request.Bytes()

	for _, target := range urlTargetList {
		connectionID, ok := target.RVConnectionID()
		if !ok {
			continue
		}

		peer := endpoint.FindConnectionByID(connectionID)
		if peer == nil {
			globals.Logger.Warningf("RequestProbeInitiation: no connection with RVCID %d", connectionID)
			continue
		}

		var probe nex.PRUDPPacketInterface

		switch peer.DefaultPRUDPVersion {
		case 0:
			probe, _ = nex.NewPRUDPPacketV0(server, peer, nil)
		case 1:
			probe, _ = nex.NewPRUDPPacketV1(server, peer, nil)
		case 2:
			probe, _ = nex.NewPRUDPPacketLite(server, peer, nil)
		default:
			globals.Logger.Errorf("RequestProbeInitiation: unsupported PRUDP version %d", peer.DefaultPRUDPVersion)
			continue
		}

		probe.SetType(constants.DataPacket)
		probe.AddFlag(constants.PacketFlagNeedsAck)
		probe.AddFlag(constants.PacketFlagReliable)
		probe.SetSourceVirtualPortStreamType(peer.StreamType)
		probe.SetSourceVirtualPortStreamID(endpoint.StreamID)
		probe.SetDestinationVirtualPortStreamType(peer.StreamType)
		probe.SetDestinationVirtualPortStreamID(peer.StreamID)
		probe.SetPayload(requestBytes)

		server.Send(probe)
	}

	response := nex.NewRMCSuccess(endpoint, nil)
	response.ProtocolID = nattraversal.ProtocolID
	response.MethodID = nattraversal.MethodRequestProbeInitiation
	response.CallID = callID

	return response, nil
}

// * ---------------------------------------------------------------------------
// * Ticket Granting extras
// * ---------------------------------------------------------------------------

// getPID resolves a username to a PID.
//
// Response: PID.
func getPID(err error, packet nex.PacketInterface, callID uint32, strUserName types.String) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Core.InvalidArgument, err.Error())
	}

	connection := packet.Sender().(*nex.PRUDPConnection)
	endpoint := connection.Endpoint().(*nex.PRUDPEndPoint)

	account, nexError := globals.AccountDetailsByUsername(string(strUserName))
	if nexError != nil {
		return nil, nexError
	}

	stream := nex.NewByteStreamOut(endpoint.LibraryVersions(), endpoint.ByteStreamSettings())
	account.PID.WriteTo(stream)

	response := nex.NewRMCSuccess(endpoint, stream.Bytes())
	response.ProtocolID = ticketgranting.ProtocolID
	response.MethodID = ticketgranting.MethodGetPID
	response.CallID = callID

	return response, nil
}

// getName resolves a PID to a username.
//
// Response: String.
func getName(err error, packet nex.PacketInterface, callID uint32, id types.PID) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Core.InvalidArgument, err.Error())
	}

	connection := packet.Sender().(*nex.PRUDPConnection)
	endpoint := connection.Endpoint().(*nex.PRUDPEndPoint)

	account, nexError := globals.AccountDetailsByPID(id)
	if nexError != nil {
		return nil, nexError
	}

	stream := nex.NewByteStreamOut(endpoint.LibraryVersions(), endpoint.ByteStreamSettings())
	types.NewString(account.Username).WriteTo(stream)

	response := nex.NewRMCSuccess(endpoint, stream.Bytes())
	response.ProtocolID = ticketgranting.ProtocolID
	response.MethodID = ticketgranting.MethodGetName
	response.CallID = callID

	return response, nil
}

// * ---------------------------------------------------------------------------
// * Tracing
// * ---------------------------------------------------------------------------

// traceHandler logs every RMC request that reaches an endpoint.
//
// No packet capture of this game's traffic has ever been published, so when
// something does not work the trace is the only specification available: it
// shows exactly which protocol and method the console called and with what
// bytes. Enable it with FAST_LOG_PACKETS=true.
func traceHandler(tag string) func(packet nex.PacketInterface) {
	return func(packet nex.PacketInterface) {
		if !globals.Settings.LogPackets {
			return
		}

		request := packet.RMCMessage()
		connection := packet.Sender().(*nex.PRUDPConnection)

		globals.Logger.Infof(
			"[%s] pid=%d protocol=%#x method=%#x call=%d params=%d bytes %s",
			tag,
			uint64(connection.PID()),
			request.ProtocolID,
			request.MethodID,
			request.CallID,
			len(request.Parameters),
			hex.EncodeToString(request.Parameters),
		)
	}
}

package nexserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"sync"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	utility "github.com/PretendoNetwork/nex-protocols-go/v2/utility"
	utilitytypes "github.com/PretendoNetwork/nex-protocols-go/v2/utility/types"
	"github.com/ProtariumNetwork/fast-racing-neo/globals"
)

// FAST links nexut and uses Utility IDs as stable per-account handles. Keeping
// them deterministic makes them survive restarts without adding a second
// database next to the already persistent ranking store.
var utilityAssociations = struct {
	sync.RWMutex
	byPID map[uint64]types.List[utilitytypes.UniqueIDInfo]
}{byPID: make(map[uint64]types.List[utilitytypes.UniqueIDInfo])}

func registerUtilityHandlers(protocol *utility.Protocol) {
	protocol.SetHandlerAcquireNexUniqueID(acquireNexUniqueID)
	protocol.SetHandlerAcquireNexUniqueIDWithPassword(acquireNexUniqueIDWithPassword)
	protocol.SetHandlerAssociateNexUniqueIDWithMyPrincipalID(associateNexUniqueID)
	protocol.SetHandlerAssociateNexUniqueIDsWithMyPrincipalID(associateNexUniqueIDs)
	protocol.SetHandlerGetAssociatedNexUniqueIDWithMyPrincipalID(getAssociatedNexUniqueID)
	protocol.SetHandlerGetAssociatedNexUniqueIDsWithMyPrincipalID(getAssociatedNexUniqueIDs)
	protocol.SetHandlerGetIntegerSettings(getIntegerSettings)
	protocol.SetHandlerGetStringSettings(getStringSettings)
}

func utilityID(pid types.PID) utilitytypes.UniqueIDInfo {
	secret := []byte(globals.Settings.AccountSecret)
	pidBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(pidBytes, uint64(pid))

	digest := hmac.New(sha256.New, secret)
	digest.Write([]byte("fast-racing-neo:unique-id:"))
	digest.Write(pidBytes)
	id := binary.LittleEndian.Uint64(digest.Sum(nil)[:8])
	if id == 0 {
		id = uint64(pid) + 1
	}

	digest = hmac.New(sha256.New, secret)
	digest.Write([]byte("fast-racing-neo:unique-password:"))
	digest.Write(pidBytes)
	password := binary.LittleEndian.Uint64(digest.Sum(nil)[:8])

	info := utilitytypes.NewUniqueIDInfo()
	info.NEXUniqueID = types.NewUInt64(id)
	info.NEXUniqueIDPassword = types.NewUInt64(password)
	return info
}

func utilitySuccess(packet nex.PacketInterface, callID uint32, methodID uint32, write func(*nex.ByteStreamOut)) (*nex.RMCMessage, *nex.Error) {
	endpoint := packet.Sender().Endpoint()
	stream := nex.NewByteStreamOut(endpoint.LibraryVersions(), endpoint.ByteStreamSettings())
	if write != nil {
		write(stream)
	}
	response := nex.NewRMCSuccess(endpoint, stream.Bytes())
	response.ProtocolID = utility.ProtocolID
	response.MethodID = methodID
	response.CallID = callID
	return response, nil
}

func acquireNexUniqueID(err error, packet nex.PacketInterface, callID uint32) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Core.InvalidArgument, err.Error())
	}
	info := utilityID(packet.Sender().PID())
	return utilitySuccess(packet, callID, utility.MethodAcquireNexUniqueID, func(stream *nex.ByteStreamOut) {
		info.NEXUniqueID.WriteTo(stream)
	})
}

func acquireNexUniqueIDWithPassword(err error, packet nex.PacketInterface, callID uint32) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Core.InvalidArgument, err.Error())
	}
	info := utilityID(packet.Sender().PID())
	return utilitySuccess(packet, callID, utility.MethodAcquireNexUniqueIDWithPassword, func(stream *nex.ByteStreamOut) {
		info.WriteTo(stream)
	})
}

func associateNexUniqueID(err error, packet nex.PacketInterface, callID uint32, info utilitytypes.UniqueIDInfo) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Core.InvalidArgument, err.Error())
	}
	pid := uint64(packet.Sender().PID())
	utilityAssociations.Lock()
	if info.NEXUniqueID == 0 {
		delete(utilityAssociations.byPID, pid)
	} else {
		utilityAssociations.byPID[pid] = types.List[utilitytypes.UniqueIDInfo]{info}
	}
	utilityAssociations.Unlock()
	return utilitySuccess(packet, callID, utility.MethodAssociateNexUniqueIDWithMyPrincipalID, nil)
}

func associateNexUniqueIDs(err error, packet nex.PacketInterface, callID uint32, infos types.List[utilitytypes.UniqueIDInfo]) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Core.InvalidArgument, err.Error())
	}
	pid := uint64(packet.Sender().PID())
	utilityAssociations.Lock()
	utilityAssociations.byPID[pid] = append(types.List[utilitytypes.UniqueIDInfo](nil), infos...)
	utilityAssociations.Unlock()
	return utilitySuccess(packet, callID, utility.MethodAssociateNexUniqueIDsWithMyPrincipalID, nil)
}

func associatedUtilityIDs(pid types.PID) types.List[utilitytypes.UniqueIDInfo] {
	utilityAssociations.RLock()
	infos := append(types.List[utilitytypes.UniqueIDInfo](nil), utilityAssociations.byPID[uint64(pid)]...)
	utilityAssociations.RUnlock()
	if len(infos) == 0 {
		infos = types.List[utilitytypes.UniqueIDInfo]{utilityID(pid)}
	}
	return infos
}

func getAssociatedNexUniqueID(err error, packet nex.PacketInterface, callID uint32) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Core.InvalidArgument, err.Error())
	}
	info := associatedUtilityIDs(packet.Sender().PID())[0]
	return utilitySuccess(packet, callID, utility.MethodGetAssociatedNexUniqueIDWithMyPrincipalID, func(stream *nex.ByteStreamOut) {
		info.WriteTo(stream)
	})
}

func getAssociatedNexUniqueIDs(err error, packet nex.PacketInterface, callID uint32) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Core.InvalidArgument, err.Error())
	}
	infos := associatedUtilityIDs(packet.Sender().PID())
	return utilitySuccess(packet, callID, utility.MethodGetAssociatedNexUniqueIDsWithMyPrincipalID, func(stream *nex.ByteStreamOut) {
		infos.WriteTo(stream)
	})
}

func getIntegerSettings(err error, packet nex.PacketInterface, callID uint32, _ types.UInt32) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Core.InvalidArgument, err.Error())
	}
	settings := types.NewMap[types.UInt16, types.UInt32]()
	return utilitySuccess(packet, callID, utility.MethodGetIntegerSettings, func(stream *nex.ByteStreamOut) {
		settings.WriteTo(stream)
	})
}

func getStringSettings(err error, packet nex.PacketInterface, callID uint32, _ types.UInt32) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Core.InvalidArgument, err.Error())
	}
	settings := types.NewMap[types.UInt16, types.String]()
	return utilitySuccess(packet, callID, utility.MethodGetStringSettings, func(stream *nex.ByteStreamOut) {
		settings.WriteTo(stream)
	})
}

package matchmaking

import (
	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	matchmakingext "github.com/PretendoNetwork/nex-protocols-go/v2/match-making-ext"
	matchmakingtypes "github.com/PretendoNetwork/nex-protocols-go/v2/match-making/types"
	notificationsconstants "github.com/PretendoNetwork/nex-protocols-go/v2/notifications/constants"
)

// * MatchMakingExt (0x32) is a small extension protocol with six methods. The
// * important one is GetParticipantsURLs: it is how a console learns where its
// * opponents actually are on the network, which is what lets the race itself
// * run peer to peer while this server only brokers the lobby.

func (p *Protocol) registerMatchMakingExt(protocol *matchmakingext.Protocol) {
	protocol.SetHandlerEndParticipation(p.endParticipation)
	protocol.SetHandlerGetParticipants(p.extGetParticipants)
	protocol.SetHandlerGetDetailedParticipants(p.extGetDetailedParticipants)
	protocol.SetHandlerGetParticipantsURLs(p.extGetParticipantsURLs)
	protocol.SetHandlerDeleteFromDeletions(p.extDeleteFromDeletions)
}

// endParticipation leaves a gathering gracefully.
//
// Response: Bool retval.
func (p *Protocol) endParticipation(err error, packet nex.PacketInterface, callID uint32, idGathering types.UInt32, strMessage types.String) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, invalidArgument(err)
	}

	if len(strMessage) > maxMessageLength {
		return nil, invalidArgument(nil)
	}

	connection, endpoint := connectionOf(packet)

	p.Store.Lock()

	session, nexError := p.Store.GetLocked(uint32(idGathering))
	if nexError != nil {
		p.Store.Unlock()
		return nil, nexError
	}

	p.Store.LeaveLocked(session, uint64(connection.PID()), notificationsconstants.ParticipationEventsEndParticipation, string(strMessage))

	p.Store.Unlock()

	p.Logger.Infof("PID %d left gathering %d", uint64(connection.PID()), uint32(idGathering))

	stream := newStream(endpoint)
	types.NewBool(true).WriteTo(stream)

	return respond(endpoint, matchmakingext.ProtocolID, matchmakingext.MethodEndParticipation, callID, stream.Bytes()), nil
}

// extGetParticipants lists the PIDs in a gathering.
//
// bOnlyActive would exclude participants who have left but not been reaped.
// This store removes participants immediately, so every stored participant is
// active and the flag makes no difference.
//
// Response: List<PID> participants.
func (p *Protocol) extGetParticipants(err error, packet nex.PacketInterface, callID uint32, idGathering types.UInt32, bOnlyActive types.Bool) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, invalidArgument(err)
	}

	_, endpoint := connectionOf(packet)

	p.Store.RLock()
	session, nexError := p.Store.GetLocked(uint32(idGathering))
	if nexError != nil {
		p.Store.RUnlock()
		return nil, nexError
	}

	participants := participantList(session)
	p.Store.RUnlock()

	stream := newStream(endpoint)
	participants.WriteTo(stream)

	return respond(endpoint, matchmakingext.ProtocolID, matchmakingext.MethodGetParticipants, callID, stream.Bytes()), nil
}

// extGetDetailedParticipants lists the participants with their details.
//
// Response: List<ParticipantDetails> details.
func (p *Protocol) extGetDetailedParticipants(err error, packet nex.PacketInterface, callID uint32, idGathering types.UInt32, bOnlyActive types.Bool) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, invalidArgument(err)
	}

	_, endpoint := connectionOf(packet)

	p.Store.RLock()
	session, nexError := p.Store.GetLocked(uint32(idGathering))
	if nexError != nil {
		p.Store.RUnlock()
		return nil, nexError
	}

	details := participantDetails(session)
	p.Store.RUnlock()

	stream := newStream(endpoint)
	details.WriteTo(stream)

	return respond(endpoint, matchmakingext.ProtocolID, matchmakingext.MethodGetDetailedParticipants, callID, stream.Bytes()), nil
}

// extGetParticipantsURLs returns, for each requested gathering, the station
// URLs of everyone in it.
//
// This is the peer to peer handshake. The station URLs were registered by each
// console through SecureConnection::RegisterEx, where nex-go replaced the
// client-claimed public address with the address the packet actually arrived
// from - so what the game receives here is the reflected, NAT-correct address
// of every other racer.
//
// A gathering the caller is not part of is skipped rather than rejected, so one
// bad ID cannot break a batched lookup.
//
// Response: List<GatheringURLs> urls.
func (p *Protocol) extGetParticipantsURLs(err error, packet nex.PacketInterface, callID uint32, lstGatherings types.List[types.UInt32]) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, invalidArgument(err)
	}

	connection, endpoint := connectionOf(packet)

	p.Store.RLock()

	gatheringURLs := types.NewList[matchmakingtypes.GatheringURLs]()

	for _, gatheringID := range lstGatherings {
		session, nexError := p.Store.GetLocked(uint32(gatheringID))
		if nexError != nil {
			continue
		}

		if !session.Contains(uint64(connection.PID())) {
			continue
		}

		entry := matchmakingtypes.NewGatheringURLs()
		entry.GID = gatheringID
		entry.LstStationURLs = stationURLsOf(endpoint, session)

		gatheringURLs = append(gatheringURLs, entry)
	}

	p.Store.RUnlock()

	stream := newStream(endpoint)
	gatheringURLs.WriteTo(stream)

	return respond(endpoint, matchmakingext.ProtocolID, matchmakingext.MethodGetParticipantsURLs, callID, stream.Bytes()), nil
}

// extDeleteFromDeletions acknowledges gatherings the client saw disappear.
//
// This server destroys gatherings outright instead of keeping a pending
// deletions list, so there is nothing to clear - but the call must still
// succeed, since a client that gets an error here can stall.
//
// Response: no return values.
func (p *Protocol) extDeleteFromDeletions(err error, packet nex.PacketInterface, callID uint32, lstDeletions types.List[types.UInt32], pid types.PID) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, invalidArgument(err)
	}

	_, endpoint := connectionOf(packet)

	return respondEmpty(endpoint, matchmakingext.ProtocolID, matchmakingext.MethodDeleteFromDeletions, callID), nil
}

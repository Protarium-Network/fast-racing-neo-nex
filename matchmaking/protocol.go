package matchmaking

import (
	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	matchmakingprotocol "github.com/PretendoNetwork/nex-protocols-go/v2/match-making"
	matchmakingextprotocol "github.com/PretendoNetwork/nex-protocols-go/v2/match-making-ext"
	matchmakingtypes "github.com/PretendoNetwork/nex-protocols-go/v2/match-making/types"
	matchmakeextensionprotocol "github.com/PretendoNetwork/nex-protocols-go/v2/matchmake-extension"
	notificationsconstants "github.com/PretendoNetwork/nex-protocols-go/v2/notifications/constants"
	"github.com/PretendoNetwork/plogger-go"
)

// Protocol implements MatchMaking (0x15), MatchMakingExt (0x32) and
// MatchmakeExtension (0x6D) against an in-memory Store.
//
// All three protocols ship inside the game's nexmm library and nothing in the
// executable's import table says which one it actually calls, so all three are
// registered. MatchmakeExtension is Nintendo's newer path and the one a 2013
// title most likely uses; MatchMaking and MatchMakingExt are the older Quazal
// Rendez-Vous surface and are backed by the same store, so either route
// produces the same lobbies.
type Protocol struct {
	Store *Store

	// AllowPublicMatchmaking gates session discovery. With it off, sessions can
	// still be created and joined by gathering ID (friend and invite flows),
	// but browsing and automatic matchmaking return nothing.
	AllowPublicMatchmaking bool

	Logger *plogger.Logger
}

// NewProtocol returns a Protocol backed by the given store.
func NewProtocol(store *Store, allowPublicMatchmaking bool, logger *plogger.Logger) *Protocol {
	return &Protocol{
		Store:                  store,
		AllowPublicMatchmaking: allowPublicMatchmaking,
		Logger:                 logger,
	}
}

// Register attaches every handler to the three matchmaking protocols and hooks
// connection teardown so a dropped console does not leave a ghost in a lobby.
func (p *Protocol) Register(
	endpoint *nex.PRUDPEndPoint,
	matchMaking *matchmakingprotocol.Protocol,
	matchMakingExt *matchmakingextprotocol.Protocol,
	matchmakeExtension *matchmakeextensionprotocol.Protocol,
) {
	p.registerMatchmakeExtension(matchmakeExtension)
	p.registerMatchMaking(matchMaking)
	p.registerMatchMakingExt(matchMakingExt)

	// * A console that loses power or Wi-Fi never sends EndParticipation, so
	// * without this every crash would leave a phantom racer occupying a slot
	// * until the server restarted.
	endpoint.OnConnectionEnded(func(connection *nex.PRUDPConnection) {
		p.Store.RemoveConnection(uint64(connection.PID()), notificationsconstants.ParticipationEventsDisconnect)
	})
}

// * ---------------------------------------------------------------------------
// * Shared helpers
// * ---------------------------------------------------------------------------

// connectionOf extracts the PRUDP connection and endpoint from a packet.
func connectionOf(packet nex.PacketInterface) (*nex.PRUDPConnection, *nex.PRUDPEndPoint) {
	connection := packet.Sender().(*nex.PRUDPConnection)
	endpoint := connection.Endpoint().(*nex.PRUDPEndPoint)

	return connection, endpoint
}

// newStream returns an output stream configured for the endpoint's NEX version.
func newStream(endpoint *nex.PRUDPEndPoint) *nex.ByteStreamOut {
	return nex.NewByteStreamOut(endpoint.LibraryVersions(), endpoint.ByteStreamSettings())
}

// respond builds a successful RMC response carrying the given body.
func respond(endpoint *nex.PRUDPEndPoint, protocolID uint16, methodID uint32, callID uint32, body []byte) *nex.RMCMessage {
	response := nex.NewRMCSuccess(endpoint, body)
	response.ProtocolID = protocolID
	response.MethodID = methodID
	response.CallID = callID

	return response
}

// respondEmpty builds a successful RMC response with no return value.
func respondEmpty(endpoint *nex.PRUDPEndPoint, protocolID uint16, methodID uint32, callID uint32) *nex.RMCMessage {
	return respond(endpoint, protocolID, methodID, callID, nil)
}

// invalidArgument is the standard rejection for a malformed request.
func invalidArgument(err error) *nex.Error {
	if err != nil {
		return nex.NewError(nex.ResultCodes.Core.InvalidArgument, err.Error())
	}

	return nex.NewError(nex.ResultCodes.Core.InvalidArgument, "Invalid argument")
}

// holderFor wraps a MatchmakeSession in the AnyObjectHolder the protocols use.
func holderFor(session matchmakingtypes.MatchmakeSession) matchmakingtypes.GatheringHolder {
	holder := matchmakingtypes.NewGatheringHolder()
	holder.Object = session.Copy().(matchmakingtypes.GatheringInterface)

	return holder
}

// publicCopy returns a copy of a session safe to hand to a client that is not
// (yet) a participant: the session key and user password are stripped, since
// they are the credentials for the lobby rather than a description of it.
func publicCopy(session matchmakingtypes.MatchmakeSession) matchmakingtypes.MatchmakeSession {
	copied := session.Copy().(matchmakingtypes.MatchmakeSession)
	copied.SessionKey = types.NewBuffer(make([]byte, 0))
	copied.UserPassword = types.NewString("")

	return copied
}

// sessionFromHolder pulls a MatchmakeSession out of a GatheringHolder.
func sessionFromHolder(holder matchmakingtypes.GatheringHolder) (matchmakingtypes.MatchmakeSession, *nex.Error) {
	if holder.Object == nil {
		return matchmakingtypes.MatchmakeSession{}, invalidArgument(nil)
	}

	if !holder.Object.GatheringObjectID().Equals(types.NewString("MatchmakeSession")) {
		return matchmakingtypes.MatchmakeSession{}, nex.NewError(nex.ResultCodes.Core.InvalidArgument, "Gathering is not a MatchmakeSession")
	}

	session, ok := holder.Object.(matchmakingtypes.MatchmakeSession)
	if !ok {
		return matchmakingtypes.MatchmakeSession{}, nex.NewError(nex.ResultCodes.Core.InvalidArgument, "Gathering is not a MatchmakeSession")
	}

	return session, nil
}

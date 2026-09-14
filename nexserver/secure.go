package nexserver

import (
	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/ProtariumNetwork/fast-racing-neo/globals"
	"github.com/ProtariumNetwork/fast-racing-neo/matchmaking"
	"github.com/ProtariumNetwork/fast-racing-neo/ranking"
)

// Matchmaking is the live gathering store, shared by all three matchmaking
// protocols.
var Matchmaking *matchmaking.Store

// Ranking is the persistent leaderboard store.
var Ranking *ranking.Store

// StartSecureServer brings up the game server proper.
//
// A console arrives here holding a Kerberos ticket issued by the authentication
// server. nex-go validates that ticket during the PRUDP CONNECT handshake - it
// decrypts the ticket's internal data with the secure server account's key,
// checks it has not expired, and adopts the session key inside it to encrypt
// everything that follows. Only then do the game protocols become reachable.
func StartSecureServer() {
	globals.SecureServer = nex.NewPRUDPServer()

	// * Must match the authentication server exactly: same access key (packet
	// * signatures), same NEX version (wire format), same session key length
	// * (the secure server reads back what the auth server wrote into the
	// * Kerberos ticket).
	// * These four must be identical to the authentication server's. They are
	// * set from the same constants rather than read off the other server,
	// * because the two start concurrently and neither may observe the other
	// * mid-construction.
	// *
	// *   AccessKey             every packet signature derives from it
	// *   LibraryVersions       the wire format of every structure
	// *   SessionKeyLength      the secure server reads back exactly this many
	// *                         bytes of session key from the Kerberos ticket
	// *                         the authentication server wrote
	// *   KerberosTicketVersion the framing of that ticket's internal data
	globals.SecureServer.ByteStreamSettings.UseStructureHeader = true
	globals.SecureServer.LibraryVersions.SetDefault(nex.NewLibraryVersion(
		globals.NEXVersionMajor,
		globals.NEXVersionMinor,
		globals.NEXVersionPatch,
	))
	globals.SecureServer.AccessKey = globals.Settings.AccessKey
	globals.SecureServer.SessionKeyLength = sessionKeyLength
	globals.SecureServer.KerberosTicketVersion = kerberosTicketVersion

	globals.SecureEndpoint = nex.NewPRUDPEndPoint(1)

	// * The one structural difference from the authentication endpoint: this
	// * endpoint demands a Kerberos ticket on connect and encrypts payloads
	// * with the session key from it.
	globals.SecureEndpoint.IsSecureEndPoint = true

	globals.SecureEndpoint.ServerAccount = globals.SecureServerAccount
	globals.SecureEndpoint.AccountDetailsByPID = globals.AccountDetailsByPID
	globals.SecureEndpoint.AccountDetailsByUsername = globals.AccountDetailsByUsername

	globals.SecureServer.BindPRUDPEndPoint(globals.SecureEndpoint)

	// * Registered before the protocols so it sees every packet, including the
	// * ones no handler claims. OnData handlers are a slice and all of them
	// * fire, so this does not displace protocol dispatch.
	globals.SecureEndpoint.OnData(detectStructureHeader)
	globals.SecureEndpoint.OnData(traceHandler("SECURE"))

	globals.SecureEndpoint.OnError(func(err *nex.Error) {
		globals.Logger.Errorf("[SECURE] %v", err)
	})

	globals.SecureEndpoint.OnConnectionEnded(func(connection *nex.PRUDPConnection) {
		globals.Logger.Infof("PID %d disconnected", uint64(connection.PID()))
	})

	Matchmaking = matchmaking.NewStore(globals.SecureEndpoint)
	Matchmaking.GetUserFriendPIDs = globals.GetUserFriendPIDs
	Ranking.GetUserFriendPIDs = globals.GetUserFriendPIDs

	registerSecureProtocols()

	globals.Logger.Successf("Secure server listening on UDP :%d", globals.Settings.SecureServerPort)

	globals.SecureServer.Listen(globals.Settings.SecureServerPort)
}

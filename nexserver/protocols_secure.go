package nexserver

import (
	commonnattraversal "github.com/PretendoNetwork/nex-protocols-common-go/v2/nat-traversal"
	commonranking "github.com/PretendoNetwork/nex-protocols-common-go/v2/ranking"
	commonsecureconnection "github.com/PretendoNetwork/nex-protocols-common-go/v2/secure-connection"
	matchmakingprotocol "github.com/PretendoNetwork/nex-protocols-go/v2/match-making"
	matchmakingextprotocol "github.com/PretendoNetwork/nex-protocols-go/v2/match-making-ext"
	matchmakeextensionprotocol "github.com/PretendoNetwork/nex-protocols-go/v2/matchmake-extension"
	nattraversalprotocol "github.com/PretendoNetwork/nex-protocols-go/v2/nat-traversal"
	rankingprotocol "github.com/PretendoNetwork/nex-protocols-go/v2/ranking"
	secureconnectionprotocol "github.com/PretendoNetwork/nex-protocols-go/v2/secure-connection"
	utilityprotocol "github.com/PretendoNetwork/nex-protocols-go/v2/utility"
	"github.com/ProtariumNetwork/fast-racing-neo/globals"
	"github.com/ProtariumNetwork/fast-racing-neo/matchmaking"
)

// registerSecureProtocols wires every protocol the game can reach onto the
// secure endpoint.
//
// The protocol set is not guesswork: kinnay's RPX-derived library index shows
// every FAST Racing NEO revision links exactly four NEX libraries -
//
//	nex    3.9.1   core: Secure Connection (0xB), NAT Traversal (0x3)
//	nexmm  3.9.1   MatchMaking (0x15), MatchMakingExt (0x32), MatchmakeExtension (0x6D)
//	nexrk  3.9.1   Ranking (0x70)
//	nexut  3.9.1   Utility (0x6E)
func registerSecureProtocols() {
	registerSecureConnection()
	registerNATTraversal()
	registerMatchmaking()
	registerRanking()
	registerUtility()
}

// registerUtility wires nexut's Utility protocol (0x6E). FAST Racing NEO links
// nexut 3.9.1 and uses its per-player stable identifiers.
func registerUtility() {
	protocol := utilityprotocol.NewProtocol()
	globals.SecureEndpoint.RegisterServiceProtocol(protocol)
	registerUtilityHandlers(protocol)
}

// registerSecureConnection wires Secure Connection (0xB).
//
// This is where a console publishes its station URLs - the addresses other
// consoles will use to reach it directly during a race. common-go's Register
// and RegisterEx already do the important part: they overwrite the client's
// claimed public address with the address the packet actually arrived from, so
// a console behind NAT advertises its real external address rather than its
// LAN address.
func registerSecureConnection() {
	protocol := secureconnectionprotocol.NewProtocol()
	globals.SecureEndpoint.RegisterServiceProtocol(protocol)

	common := commonsecureconnection.NewCommonProtocol(protocol)

	// * RegisterEx refuses to run without a validator, exactly as LoginEx does.
	common.ValidateLoginData = validateLoginData

	// * SendReport carries the game's own telemetry/abuse reports. There is no
	// * report database here, so they are logged and acknowledged.
	common.CreateReportDBRecord = createReportRecord

	// * FAST may still call the plain Register during connection recovery.
	// * Enabling it costs nothing:
	// * both paths do the same station URL reflection.
	common.EnableInsecureRegister()

	// * The three methods common-go does not implement.
	protocol.SetHandlerRequestConnectionData(requestConnectionData)
	protocol.SetHandlerTestConnectivity(testConnectivity)
	protocol.SetHandlerUpdateURLs(updateURLs)
}

// registerNATTraversal wires NAT Traversal (0x3).
//
// The server's role here is to relay probe requests between consoles so they
// can punch a hole through each other's NAT. common-go implements everything
// except the older non-Ext probe request, which FAST can use during NAT setup.
func registerNATTraversal() {
	protocol := nattraversalprotocol.NewProtocol()
	globals.SecureEndpoint.RegisterServiceProtocol(protocol)

	commonnattraversal.NewCommonProtocol(protocol)

	protocol.SetHandlerRequestProbeInitiation(requestProbeInitiation)
}

// registerMatchmaking wires all three matchmaking protocols to the shared
// in-memory store.
func registerMatchmaking() {
	matchMaking := matchmakingprotocol.NewProtocol()
	globals.SecureEndpoint.RegisterServiceProtocol(matchMaking)

	matchMakingExt := matchmakingextprotocol.NewProtocol()
	globals.SecureEndpoint.RegisterServiceProtocol(matchMakingExt)

	matchmakeExtension := matchmakeextensionprotocol.NewProtocol()
	globals.SecureEndpoint.RegisterServiceProtocol(matchmakeExtension)

	protocol := matchmaking.NewProtocol(Matchmaking, globals.Settings.AllowPublicMatchmaking, globals.Logger)
	protocol.Register(globals.SecureEndpoint, matchMaking, matchMakingExt, matchmakeExtension)

	if !globals.Settings.AllowPublicMatchmaking {
		globals.Logger.Warning("Public matchmaking is disabled; only direct joins by gathering ID will work")
	}
}

// registerRanking wires Ranking (0x70) to the persistent leaderboard store.
//
// common-go's ranking protocol is entirely callback driven - no database, no
// manager - so the store plugs straight in.
func registerRanking() {
	protocol := rankingprotocol.NewProtocol()
	globals.SecureEndpoint.RegisterServiceProtocol(protocol)

	common := commonranking.NewCommonProtocol(protocol)

	common.GetCommonData = Ranking.GetCommonData
	common.UploadCommonData = Ranking.UploadCommonData
	common.InsertRankingByPIDAndRankingScoreData = Ranking.InsertRankingByPIDAndRankingScoreData
	common.GetRankingsAndCountByCategoryAndRankingOrderParam = Ranking.GetRankingsAndCountByCategoryAndRankingOrderParam
	common.GetNearbyRankingsAndCountByCategoryAndRankingOrderParam = Ranking.GetNearbyRankingsAndCountByCategoryAndRankingOrderParam
	common.GetFriendsRankingsAndCountByCategoryAndRankingOrderParam = Ranking.GetFriendsRankingsAndCountByCategoryAndRankingOrderParam
	common.GetNearbyFriendsRankingsAndCountByCategoryAndRankingOrderParam = Ranking.GetNearbyFriendsRankingsAndCountByCategoryAndRankingOrderParam
	common.GetOwnRankingByCategoryAndRankingOrderParam = Ranking.GetOwnRankingByCategoryAndRankingOrderParam

	// * The methods common-go leaves unregistered.
	protocol.SetHandlerDeleteScore(deleteScore)
	protocol.SetHandlerDeleteAllScores(deleteAllScores)
	protocol.SetHandlerDeleteCommonData(deleteCommonData)
	protocol.SetHandlerGetRankingByPIDList(getRankingByPIDList)
	protocol.SetHandlerGetApproxOrder(getApproxOrder)
}

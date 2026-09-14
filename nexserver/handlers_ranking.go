package nexserver

import (
	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	rankingprotocol "github.com/PretendoNetwork/nex-protocols-go/v2/ranking"
	rankingconstants "github.com/PretendoNetwork/nex-protocols-go/v2/ranking/constants"
	rankingtypes "github.com/PretendoNetwork/nex-protocols-go/v2/ranking/types"
	"github.com/ProtariumNetwork/fast-racing-neo/globals"
)

// * Ranking methods left unregistered by nex-protocols-common-go. FAST Racing
// * NEO needs the complete surface for leaderboards and time-trial data.

// deleteScore removes the caller's score in one category.
//
// Response: no return values.
func deleteScore(err error, packet nex.PacketInterface, callID uint32, category types.UInt32, uniqueID types.UInt64) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Ranking.InvalidArgument, err.Error())
	}

	connection := packet.Sender().(*nex.PRUDPConnection)
	endpoint := connection.Endpoint().(*nex.PRUDPEndPoint)

	if deleteErr := Ranking.DeleteScore(uint64(connection.PID()), uint32(category)); deleteErr != nil {
		globals.Logger.Errorf("Failed to delete score: %v", deleteErr)
		return nil, nex.NewError(nex.ResultCodes.Ranking.Unknown, deleteErr.Error())
	}

	response := nex.NewRMCSuccess(endpoint, nil)
	response.ProtocolID = rankingprotocol.ProtocolID
	response.MethodID = rankingprotocol.MethodDeleteScore
	response.CallID = callID

	return response, nil
}

// deleteAllScores removes every score the caller holds.
//
// Response: no return values.
func deleteAllScores(err error, packet nex.PacketInterface, callID uint32, uniqueID types.UInt64) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Ranking.InvalidArgument, err.Error())
	}

	connection := packet.Sender().(*nex.PRUDPConnection)
	endpoint := connection.Endpoint().(*nex.PRUDPEndPoint)

	if deleteErr := Ranking.DeleteAllScores(uint64(connection.PID())); deleteErr != nil {
		globals.Logger.Errorf("Failed to delete scores: %v", deleteErr)
		return nil, nex.NewError(nex.ResultCodes.Ranking.Unknown, deleteErr.Error())
	}

	response := nex.NewRMCSuccess(endpoint, nil)
	response.ProtocolID = rankingprotocol.ProtocolID
	response.MethodID = rankingprotocol.MethodDeleteAllScores
	response.CallID = callID

	return response, nil
}

// deleteCommonData removes the blob stored against a unique ID.
//
// Response: no return values.
func deleteCommonData(err error, packet nex.PacketInterface, callID uint32, uniqueID types.UInt64) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Ranking.InvalidArgument, err.Error())
	}

	connection := packet.Sender().(*nex.PRUDPConnection)
	endpoint := connection.Endpoint().(*nex.PRUDPEndPoint)

	if deleteErr := Ranking.DeleteCommonData(uint64(uniqueID)); deleteErr != nil {
		globals.Logger.Errorf("Failed to delete common data: %v", deleteErr)
		return nil, nex.NewError(nex.ResultCodes.Ranking.Unknown, deleteErr.Error())
	}

	response := nex.NewRMCSuccess(endpoint, nil)
	response.ProtocolID = rankingprotocol.ProtocolID
	response.MethodID = rankingprotocol.MethodDeleteCommonData
	response.CallID = callID

	return response, nil
}

// getRankingByPIDList returns the ranks of a specific set of players. This is
// what a "compare with friends" leaderboard view calls.
//
// Response: RankingResult.
func getRankingByPIDList(err error, packet nex.PacketInterface, callID uint32, principalIDList types.List[types.PID], rankingMode rankingconstants.RankingMode, category types.UInt32, orderParam rankingtypes.RankingOrderParam, uniqueID types.UInt64) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Ranking.InvalidArgument, err.Error())
	}

	connection := packet.Sender().(*nex.PRUDPConnection)
	endpoint := connection.Endpoint().(*nex.PRUDPEndPoint)

	pids := make([]uint64, 0, len(principalIDList))
	for _, pid := range principalIDList {
		pids = append(pids, uint64(pid))
	}

	rankData, totalCount := Ranking.RankingsForPIDs(pids, uint32(category), orderParam)

	result := rankingtypes.NewRankingResult()
	result.RankDataList = rankData
	result.TotalCount = types.NewUInt32(totalCount)

	// * 2000-01-01T00:00:00.000Z, which is what the real servers returned.
	result.SinceTime = types.NewDateTime(0x1F40420000)

	stream := nex.NewByteStreamOut(endpoint.LibraryVersions(), endpoint.ByteStreamSettings())
	result.WriteTo(stream)

	response := nex.NewRMCSuccess(endpoint, stream.Bytes())
	response.ProtocolID = rankingprotocol.ProtocolID
	response.MethodID = rankingprotocol.MethodGetRankingByPIDList
	response.CallID = callID

	return response, nil
}

// getApproxOrder answers "what rank would this score place at?" without
// uploading it, which is how a game shows a projected position at the end of a
// race before the player commits the time.
//
// Response: Uint32 order.
func getApproxOrder(err error, packet nex.PacketInterface, callID uint32, category types.UInt32, orderParam rankingtypes.RankingOrderParam, score types.UInt32, uniqueID types.UInt64, principalID types.PID) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Ranking.InvalidArgument, err.Error())
	}

	connection := packet.Sender().(*nex.PRUDPConnection)
	endpoint := connection.Endpoint().(*nex.PRUDPEndPoint)

	order := Ranking.ApproximateOrder(uint32(category), uint32(score))

	stream := nex.NewByteStreamOut(endpoint.LibraryVersions(), endpoint.ByteStreamSettings())
	types.NewUInt32(order).WriteTo(stream)

	response := nex.NewRMCSuccess(endpoint, stream.Bytes())
	response.ProtocolID = rankingprotocol.ProtocolID
	response.MethodID = rankingprotocol.MethodGetApproxOrder
	response.CallID = callID

	return response, nil
}

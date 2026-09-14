package nexserver

import (
	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	secureconnection "github.com/PretendoNetwork/nex-protocols-go/v2/secure-connection"
	"github.com/ProtariumNetwork/fast-racing-neo/globals"
)

// * The three Secure Connection methods nex-protocols-common-go does not
// * implement. All are small, but leaving them unregistered means the console
// * gets Core.NotImplemented back, and a client that cannot resolve a peer's
// * connection data will not start a race.

// requestConnectionData returns the station URLs and connection ID of a peer.
//
// Response: Bool retval, List<ConnectionData>.
//
// ConnectionData has no definition in nex-protocols-go, so the response body is
// written field by field. That is exact rather than approximate: at NEX 3.0.1
// structures carry no version/length header, so a List<ConnectionData> on the
// wire is simply a u32 count followed by each entry's fields back to back.
func requestConnectionData(err error, packet nex.PacketInterface, callID uint32, cidTarget types.UInt32, pidTarget types.PID) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Core.InvalidArgument, err.Error())
	}

	connection := packet.Sender().(*nex.PRUDPConnection)
	endpoint := connection.Endpoint().(*nex.PRUDPEndPoint)

	target := endpoint.FindConnectionByID(uint32(cidTarget))
	if target == nil {
		target = endpoint.FindConnectionByPID(uint64(pidTarget))
	}

	stream := nex.NewByteStreamOut(endpoint.LibraryVersions(), endpoint.ByteStreamSettings())

	if target == nil {
		globals.Logger.Warningf("RequestConnectionData: no connection for CID %d / PID %d", uint32(cidTarget), uint64(pidTarget))

		types.NewBool(false).WriteTo(stream)
		stream.WriteUInt32LE(0)
	} else {
		types.NewBool(true).WriteTo(stream)

		// * One ConnectionData entry per station URL the peer registered.
		stream.WriteUInt32LE(uint32(len(target.StationURLs)))

		for _, stationURL := range target.StationURLs {
			stationURL.WriteTo(stream)
			types.NewUInt32(target.ID).WriteTo(stream)
		}
	}

	response := nex.NewRMCSuccess(endpoint, stream.Bytes())
	response.ProtocolID = secureconnection.ProtocolID
	response.MethodID = secureconnection.MethodRequestConnectionData
	response.CallID = callID

	return response, nil
}

// testConnectivity is a round trip probe. Reaching this handler at all is the
// answer the client wants, so it returns an empty success.
//
// Response: no return values.
func testConnectivity(err error, packet nex.PacketInterface, callID uint32) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Core.InvalidArgument, err.Error())
	}

	connection := packet.Sender().(*nex.PRUDPConnection)
	endpoint := connection.Endpoint().(*nex.PRUDPEndPoint)

	response := nex.NewRMCSuccess(endpoint, nil)
	response.ProtocolID = secureconnection.ProtocolID
	response.MethodID = secureconnection.MethodTestConnectivity
	response.CallID = callID

	return response, nil
}

// updateURLs replaces the caller's registered station URLs.
//
// A console calls this when its network situation changes - for instance after
// NAT traversal discovers a working external address. As with RegisterEx, the
// public address is corrected to the address the packet actually came from, so
// a stale or wrong self-reported address cannot break peer connectivity.
//
// Response: no return values.
func updateURLs(err error, packet nex.PacketInterface, callID uint32, vecMyURLs types.List[types.StationURL]) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.Core.InvalidArgument, err.Error())
	}

	connection := packet.Sender().(*nex.PRUDPConnection)
	endpoint := connection.Endpoint().(*nex.PRUDPEndPoint)

	updated := make(types.List[types.StationURL], 0, len(vecMyURLs))

	for _, stationURL := range vecMyURLs {
		station := stationURL.Copy().(types.StationURL)

		station.SetPrincipalID(connection.PID())
		station.SetRVConnectionID(connection.ID)

		if station.IsPublic() {
			address, port := observedAddress(connection)
			if address != "" {
				station.SetAddress(address)
				station.SetPortNumber(port)
			}
		}

		updated = append(updated, station)
	}

	connection.StationURLs = updated

	if globals.Settings.LogPackets {
		globals.Logger.Infof("PID %d updated %d station URL(s)", uint64(connection.PID()), len(updated))
	}

	response := nex.NewRMCSuccess(endpoint, nil)
	response.ProtocolID = secureconnection.ProtocolID
	response.MethodID = secureconnection.MethodUpdateURLs
	response.CallID = callID

	return response, nil
}

// createReportRecord handles SecureConnection::SendReport.
//
// The game sends diagnostic and abuse reports here. There is no report store,
// so they are logged and acknowledged - returning an error would make the
// client retry indefinitely.
func createReportRecord(pid types.PID, reportID types.UInt32, reportData types.QBuffer) error {
	globals.Logger.Infof("Report from PID %d: id=%d, %d bytes", uint64(pid), uint32(reportID), len(reportData))

	return nil
}

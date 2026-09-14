package nexserver

import (
	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/constants"
	"github.com/PretendoNetwork/nex-go/v2/types"
	commonticketgranting "github.com/PretendoNetwork/nex-protocols-common-go/v2/ticket-granting"
	ticketgranting "github.com/PretendoNetwork/nex-protocols-go/v2/ticket-granting"
	"github.com/ProtariumNetwork/fast-racing-neo/globals"
)

// registerAuthenticationProtocols wires up Ticket Granting (0xA) on the
// authentication endpoint.
func registerAuthenticationProtocols() {
	ticketGrantingProtocol := ticketgranting.NewProtocol()
	globals.AuthenticationEndpoint.RegisterServiceProtocol(ticketGrantingProtocol)

	commonTicketGranting := commonticketgranting.NewCommonProtocol(ticketGrantingProtocol)

	// * The station URL below is what the console is told to connect to next.
	// * Its shape matters: the game parses the individual parameters, not just
	// * the address, so all of them have to be present.
	// *
	// *   prudps:/address=<host>;port=<port>;CID=1;PID=2;sid=1;stream=10;type=2
	// *
	// * PID=2 is the secure server account, sid=1 the endpoint stream ID,
	// * stream=10 the RVSecure stream type and type=2 the "public" flag.
	secureStationURL := types.NewStationURL("")
	secureStationURL.SetURLType(constants.StationURLPRUDPS)
	secureStationURL.SetAddress(globals.Settings.SecureServerHost)
	secureStationURL.SetPortNumber(uint16(globals.Settings.SecureServerPort))
	secureStationURL.SetConnectionID(1)
	secureStationURL.SetPrincipalID(globals.SecureServerAccount.PID)
	secureStationURL.SetStreamID(1)
	secureStationURL.SetStreamType(constants.StreamTypeRVSecure)
	secureStationURL.SetType(uint8(constants.StationURLFlagPublic))

	commonTicketGranting.SecureStationURL = secureStationURL
	commonTicketGranting.SecureServerAccount = globals.SecureServerAccount

	// * Echoed back to the console as strReturnMsg. Real NEX servers return
	// * their build string here and the game logs it, so use the genuine one
	// * for this title.
	commonTicketGranting.BuildName = types.NewString(globals.BuildName)

	// * NEX 3.x uses a 32 byte session key. (NEX 1.x games such as the Wii U
	// * friends server use 16 - getting this wrong desynchronises the whole
	// * Kerberos exchange.)
	commonTicketGranting.SessionKeyLength = 32

	// * LoginEx refuses to run without a validator. Pretendo's implementation
	// * exchanges the login token with their account server over gRPC; this
	// * server is standalone, so it accepts any well-formed login data and
	// * lets Kerberos do the actual authentication.
	commonTicketGranting.ValidateLoginData = validateLoginData

	// * The two Ticket Granting methods common-go does not register.
	ticketGrantingProtocol.SetHandlerGetPID(getPID)
	ticketGrantingProtocol.SetHandlerGetName(getName)
}

// validateLoginData performs the structural checks on a console's login blob
// that a standalone server can meaningfully make.
//
// The Wii U sends a NintendoLoginData structure containing an NNAS-issued
// token. Verifying that token requires the account server that minted it, which
// this server deliberately does not depend on. Authentication still holds:
// the Kerberos ticket returned by LoginEx is encrypted with a key derived from
// the account password, so a console that does not have the right password
// cannot use the ticket it receives.
func validateLoginData(pid types.PID, loginData types.DataHolder) *nex.Error {
	if loginData.Object == nil {
		return nex.NewError(nex.ResultCodes.Authentication.ValidationFailed, "Missing login data")
	}

	loginDataType, ok := loginData.Object.DataObjectID().(types.String)
	if !ok {
		return nex.NewError(nex.ResultCodes.Authentication.ValidationFailed, "Login data has no type name")
	}

	switch loginDataType {
	case "NintendoLoginData", "AuthenticationInfo", "AccountExtraInfo":
		// * The three login data types a Wii U or 3DS console can send.
	default:
		globals.Logger.Errorf("Unexpected login data type %q from PID %d", string(loginDataType), uint64(pid))
		return nex.NewError(nex.ResultCodes.Authentication.ValidationFailed, "Unsupported login data type")
	}

	if globals.Settings.LogPackets {
		globals.Logger.Infof("Login from PID %d using %s", uint64(pid), string(loginDataType))
	}

	return nil
}

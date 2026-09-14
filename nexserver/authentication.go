package nexserver

import (
	"fmt"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/ProtariumNetwork/fast-racing-neo/globals"
)

// * Kerberos parameters, shared by both servers. The authentication server
// * writes the ticket and the secure server reads it back, so a mismatch in
// * either value makes every login fail at the secure connection stage.
const (
	// sessionKeyLength is 32 for NEX 3.x. NEX 1.x titles - the Wii U friends
	// server, notably - use 16, which is why this is stated rather than left
	// to a default that might be copied from the wrong example.
	sessionKeyLength = 32

	// kerberosTicketVersion selects the framing of the ticket's internal data.
	// The client never sees it: only this server writes it and only this
	// server reads it back, so the value just has to be consistent.
	kerberosTicketVersion = 0
)

// StartAuthenticationServer brings up the ticket granting server.
//
// This is the first server a console talks to. It speaks exactly one protocol -
// Ticket Granting (0xA) - and its whole job is:
//
//  1. the console calls Login/LoginEx with its NEX username (its PID as a
//     decimal string),
//  2. the server mints a Kerberos ticket encrypted with that user's key and
//     hands it back along with the secure server's station URL,
//  3. the console disconnects and reconnects to the secure server with the
//     ticket.
//
// Because the ticket is encrypted with a key derived from the account password,
// a console that does not know the password cannot decrypt it. That, and not
// the login data blob, is the actual authentication step.
func StartAuthenticationServer() {
	globals.AuthenticationServer = nex.NewPRUDPServer()

	// * Whether structures carry a version/length header is decided by the
	// * console, not by the NEX version, so it starts off and is corrected the
	// * moment the first login arrives. See structure_header.go.
	globals.AuthenticationServer.ByteStreamSettings.UseStructureHeader = true

	globals.AuthenticationServer.LibraryVersions.SetDefault(nex.NewLibraryVersion(
		globals.NEXVersionMajor,
		globals.NEXVersionMinor,
		globals.NEXVersionPatch,
	))
	globals.AuthenticationServer.AccessKey = globals.Settings.AccessKey
	globals.AuthenticationServer.SessionKeyLength = sessionKeyLength
	globals.AuthenticationServer.KerberosTicketVersion = kerberosTicketVersion

	// * Endpoints are bound to a PRUDP stream ID. Wii U game servers use
	// * stream ID 1 for the main RVSecure stream.
	globals.AuthenticationEndpoint = nex.NewPRUDPEndPoint(1)
	globals.AuthenticationEndpoint.ServerAccount = globals.AuthenticationServerAccount
	globals.AuthenticationEndpoint.AccountDetailsByPID = globals.AccountDetailsByPID
	globals.AuthenticationEndpoint.AccountDetailsByUsername = globals.AccountDetailsByUsername

	globals.AuthenticationServer.BindPRUDPEndPoint(globals.AuthenticationEndpoint)

	// * Registered before the protocols so both run before any handler parses
	// * a structure. OnData handlers are a slice and every one of them fires,
	// * in registration order.
	globals.AuthenticationEndpoint.OnData(detectStructureHeader)
	globals.AuthenticationEndpoint.OnData(traceHandler("AUTH"))

	globals.AuthenticationEndpoint.OnError(func(err *nex.Error) {
		globals.Logger.Errorf("[AUTH] %v", err)
	})

	applyStructureHeaderMode()

	registerAuthenticationProtocols()

	globals.Logger.Successf(
		"Authentication server listening on UDP :%d (access key %s, NEX %d.%d.%d)",
		globals.Settings.AuthenticationServerPort,
		globals.Settings.AccessKey,
		globals.NEXVersionMajor, globals.NEXVersionMinor, globals.NEXVersionPatch,
	)
	globals.Logger.Infof(
		"Consoles will be redirected to the secure server at %s",
		fmt.Sprintf("%s:%d", globals.Settings.SecureServerHost, globals.Settings.SecureServerPort),
	)

	globals.AuthenticationServer.Listen(globals.Settings.AuthenticationServerPort)
}

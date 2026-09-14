package globals

import (
	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/plogger-go"
)

// Logger is the process-wide logger.
var Logger *plogger.Logger

// * A NEX game server is really two PRUDP servers on two UDP ports.
// *
// * The authentication server speaks only the Ticket Granting protocol. The
// * console logs in there, receives a Kerberos ticket plus the address of the
// * secure server, then disconnects.
// *
// * The secure server is where the game actually lives. The console connects
// * with the ticket it was just issued and from then on speaks the game
// * protocols (matchmaking, NAT traversal, ranking, ...).
var (
	AuthenticationServer   *nex.PRUDPServer
	AuthenticationEndpoint *nex.PRUDPEndPoint

	SecureServer   *nex.PRUDPServer
	SecureEndpoint *nex.PRUDPEndPoint
)

// * The two special, non-user, accounts every NEX server has. The console never
// * logs in as these; they are the Kerberos "source" and "target" principals
// * used when minting tickets. Their usernames are fixed by NEX itself.
var (
	AuthenticationServerAccount *nex.Account
	SecureServerAccount         *nex.Account
)

// InitAccounts creates the two special server accounts.
func InitAccounts() {
	AuthenticationServerAccount = nex.NewAccount(types.NewPID(1), "Quazal Authentication", Settings.KerberosPassword, false)
	SecureServerAccount = nex.NewAccount(types.NewPID(2), "Quazal Rendez-Vous", Settings.KerberosPassword, false)
}

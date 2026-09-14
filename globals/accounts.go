package globals

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
)

// * How NEX accounts actually work
// *
// * A "NEX account" is separate from the console's NNID. When a Wii U wants to
// * play online it asks the account server for a NEX token:
// *
// *   GET https://account.nintendo.net/v1/api/provider/nex_token/@me?game_server_id=1012F000
// *
// * The account server replies with the game server's host and port, the user's
// * NEX PID, and a 16 character `nex_password`. The console then performs
// * Kerberos authentication against this server using that PID and password.
// *
// * The password is *issued* by the account server - it is not derived from the
// * PID by the console. (What IS derived from the PID is the Kerberos key:
// * MD5 iterated 65000 + pid%1024 times over the password, which nex-go's
// * DeriveKerberosKey already implements.)
// *
// * That leaves this server needing to agree with whatever account server the
// * client used. Two supported ways to arrange that:
// *
// *  1. FAST_ACCOUNTS_FILE - a JSON file of explicit PID/password pairs.
// *     Use this when passwords are already fixed by an existing account
// *     server, or for a small closed group.
// *
// *  2. FAST_ACCOUNT_SECRET - deterministic derivation. Any PID not listed in
// *     the accounts file gets a URL-safe HMAC-SHA256 value. An account server
// *     sharing the secret can compute the
// *     same password and hand it to consoles, so no database is needed.
// *
// * A PID in the accounts file always wins over derivation.

// guestAccountUsername and guestAccountPassword are the well-known Quazal guest
// credentials. Some titles perform a guest login before the real one.
const (
	guestAccountUsername = "guest"
	guestAccountPassword = "MMQea3n!fsik"
)

var (
	accountsMutex sync.RWMutex
	// knownAccounts maps a NEX PID to an explicitly configured password.
	knownAccounts = make(map[uint64]string)
	guestAccount  *nex.Account
)

// LoadAccounts reads the optional accounts file and prepares the guest account.
//
// The file is a JSON object mapping PID (as a string, since JSON keys are
// strings) to the account's 16 character NEX password:
//
//	{
//	  "1794841116": "6Fh2K0xNqYm1PdVw",
//	  "1794841117": "Zt8Lm3RcW9pAeQyX"
//	}
func LoadAccounts() error {
	guestAccount = nex.NewAccount(types.NewPID(100), guestAccountUsername, guestAccountPassword, false)

	if Settings.AccountsFile == "" {
		return nil
	}

	contents, err := os.ReadFile(Settings.AccountsFile)
	if err != nil {
		if os.IsNotExist(err) {
			Logger.Warningf("Accounts file %s does not exist, relying on derived passwords", Settings.AccountsFile)
			return nil
		}

		return fmt.Errorf("failed to read accounts file %s: %w", Settings.AccountsFile, err)
	}

	raw := make(map[string]string)
	if err := json.Unmarshal(contents, &raw); err != nil {
		return fmt.Errorf("failed to parse accounts file %s: %w", Settings.AccountsFile, err)
	}

	accountsMutex.Lock()
	defer accountsMutex.Unlock()

	for pidString, password := range raw {
		pid, err := strconv.ParseUint(pidString, 10, 64)
		if err != nil {
			return fmt.Errorf("accounts file %s has a non-numeric PID %q: %w", Settings.AccountsFile, pidString, err)
		}

		knownAccounts[pid] = password
	}

	Logger.Successf("Loaded %d account(s) from %s", len(knownAccounts), Settings.AccountsFile)

	return nil
}

// DerivePassword deterministically produces the NEX password for a PID from the
// configured account secret. Callers that share the secret produce identical
// output, which is what lets a paired account server hand out matching
// passwords without a shared database.
func DerivePassword(pid uint64) string {
	secret, err := hex.DecodeString(Settings.AccountSecret)
	if err != nil {
		Logger.Errorf("FAST_ACCOUNT_SECRET is not valid hexadecimal: %v", err)
		return ""
	}

	pidBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(pidBytes, pid)

	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(pidBytes)

	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// PasswordFromPID returns the Kerberos password for a user account.
func PasswordFromPID(pid uint64) (string, *nex.Error) {
	accountsMutex.RLock()
	password, ok := knownAccounts[pid]
	accountsMutex.RUnlock()

	if ok {
		return password, nil
	}

	if !Settings.AllowUnknownAccounts {
		return "", nex.NewError(nex.ResultCodes.RendezVous.InvalidUsername, fmt.Sprintf("PID %d is not a known account", pid))
	}

	return DerivePassword(pid), nil
}

// AccountDetailsByPID resolves an account from its PID. nex-go calls this while
// validating the Kerberos ticket a console presents to the secure server.
func AccountDetailsByPID(pid types.PID) (*nex.Account, *nex.Error) {
	if pid.Equals(AuthenticationServerAccount.PID) {
		return AuthenticationServerAccount, nil
	}

	if pid.Equals(SecureServerAccount.PID) {
		return SecureServerAccount, nil
	}

	if guestAccount != nil && pid.Equals(guestAccount.PID) {
		return guestAccount, nil
	}

	password, nexError := PasswordFromPID(uint64(pid))
	if nexError != nil {
		return nil, nexError
	}

	// * For NEX user accounts the username is the PID in decimal.
	return nex.NewAccount(pid, strconv.FormatUint(uint64(pid), 10), password, false), nil
}

// AccountDetailsByUsername resolves an account from its username. This is the
// path taken during TicketGranting::Login(Ex), where the console identifies
// itself by username rather than PID.
func AccountDetailsByUsername(username string) (*nex.Account, *nex.Error) {
	if username == AuthenticationServerAccount.Username {
		return AuthenticationServerAccount, nil
	}

	if username == SecureServerAccount.Username {
		return SecureServerAccount, nil
	}

	if guestAccount != nil && username == guestAccount.Username {
		return guestAccount, nil
	}

	pid, err := strconv.ParseUint(username, 10, 64)
	if err != nil {
		return nil, nex.NewError(nex.ResultCodes.RendezVous.InvalidUsername, fmt.Sprintf("Username %q is not a valid NEX PID", username))
	}

	password, nexError := PasswordFromPID(pid)
	if nexError != nil {
		return nil, nexError
	}

	return nex.NewAccount(types.NewPID(pid), username, password, false), nil
}

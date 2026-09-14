// Command fast-racing-neo runs a standalone NEX game server for FAST Racing NEO.
//
//	Game server ID   0x1012F000
//	Access key       811aa39f
//	NEX libraries    nex/nexmm/nexrk/nexut 3.9.1
//
// See README.md for how a console is pointed at it.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	// * Blank import: nex-protocols-go's init() registers the object holder
	// * types (Gathering, MatchmakeSession, NintendoLoginData, ...) without
	// * which a GatheringHolder or login DataHolder cannot be decoded.
	// * Importing a subpackage does not run it, so this must be explicit.
	_ "github.com/PretendoNetwork/nex-protocols-go/v2"

	"github.com/PretendoNetwork/plogger-go"
	"github.com/ProtariumNetwork/fast-racing-neo/globals"
	"github.com/ProtariumNetwork/fast-racing-neo/nexserver"
	"github.com/ProtariumNetwork/fast-racing-neo/ranking"
	"github.com/joho/godotenv"
)

func main() {
	globals.Logger = plogger.NewLogger()

	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		globals.Logger.Warningf("Could not read .env: %v", err)
	}

	globals.LoadConfig()

	if err := validateConfig(); err != nil {
		globals.Logger.Criticalf("%v", err)
		os.Exit(1)
	}

	globals.InitAccounts()

	if err := globals.LoadAccounts(); err != nil {
		globals.Logger.Criticalf("%v", err)
		os.Exit(1)
	}

	rankingStore, err := ranking.NewStore(globals.Settings.DataDir)
	if err != nil {
		globals.Logger.Criticalf("Failed to open the ranking store: %v", err)
		os.Exit(1)
	}

	nexserver.Ranking = rankingStore

	banner()

	// * Listen() blocks forever and panics on a socket error, so each server
	// * gets its own goroutine and the main one waits for a signal.
	go nexserver.StartAuthenticationServer()
	go nexserver.StartSecureServer()

	// StartStatsServer reads the Matchmaking singleton, which StartSecureServer
	// assigns from inside its own goroutine - wait for it before serving.
	go func() {
		for nexserver.Matchmaking == nil {
			time.Sleep(50 * time.Millisecond)
		}
		nexserver.StartStatsServer()
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	<-shutdown

	globals.Logger.Info("Shutting down")
}

// validateConfig rejects a configuration that cannot possibly work, rather than
// letting the server start and fail silently later.
func validateConfig() error {
	settings := globals.Settings

	if settings.AuthenticationServerPort < 1 || settings.AuthenticationServerPort > 65535 {
		return fmt.Errorf("FAST_AUTHENTICATION_SERVER_PORT %d is not a valid port", settings.AuthenticationServerPort)
	}

	if settings.SecureServerPort < 1 || settings.SecureServerPort > 65535 {
		return fmt.Errorf("FAST_SECURE_SERVER_PORT %d is not a valid port", settings.SecureServerPort)
	}

	if settings.AuthenticationServerPort == settings.SecureServerPort {
		return fmt.Errorf("the authentication and secure servers cannot share port %d", settings.AuthenticationServerPort)
	}

	if len(settings.AccessKey) != 8 {
		return fmt.Errorf("FAST_ACCESS_KEY %q is not 8 characters; the correct key for this game is %s", settings.AccessKey, globals.DefaultAccessKey)
	}

	if settings.SecureServerHost == "" {
		return fmt.Errorf("FAST_SECURE_SERVER_HOST must be set to an address the console can reach")
	}

	// * Without a secret, every derived password would be HMAC'd under the
	// * empty key - identical on every deployment, and therefore public. Mint a
	// * random one rather than shipping a predictable default.
	if settings.AccountSecret == "" && settings.AllowUnknownAccounts {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return fmt.Errorf("failed to generate an account secret: %w", err)
		}

		globals.Settings.AccountSecret = hex.EncodeToString(secret)

		globals.Logger.Warning("FAST_ACCOUNT_SECRET is not set, so a random one was generated for this run.")
		globals.Logger.Warning("Derived passwords will change on every restart. Set it to a fixed value,")
		globals.Logger.Warning("and give the same value to whatever account server issues NEX tokens.")
	}

	return nil
}

func banner() {
	globals.Logger.Success("FAST Racing NEO - NEX server")
	globals.Logger.Infof("  Game server ID   %08X", globals.GameServerID)
	globals.Logger.Infof("  Access key       %s", globals.Settings.AccessKey)
	globals.Logger.Infof("  NEX version      %d.%d.%d", globals.NEXVersionMajor, globals.NEXVersionMinor, globals.NEXVersionPatch)
	globals.Logger.Infof("  Build string     %s", globals.BuildName)
	globals.Logger.Infof("  Data directory   %s", globals.Settings.DataDir)
}

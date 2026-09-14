package globals

import (
	"os"
	"strconv"
	"strings"
)

// * FAST Racing NEO (Wii U) server identity.
// *
// * These values come from the NEX game server list at
// * https://kinnay.github.io/view.html?page=nexwiiu ("FAST Racing NEO"):
// *
// *   Game server ID  1012F000
// *   Title ID (EUR)  00050000101D6000
// *   Access key      811aa39f
// *   Build           branch:origin/release/ngs/3.9.x.200x build:3_9_19_2005_0
// *
// * The build string is echoed back to the console verbatim in the
// * TicketGranting::Login(Ex) response, so it must match the real one.
const (
	GameServerID = 0x1012F000
	TitleID      = 0x00050000101D6000

	DefaultAccessKey = "811aa39f"

	// * BuildName is echoed back to the console verbatim as strReturnMsg in the
	// * TicketGranting::Login(Ex) response. It is free text, not a version gate,
	// * so we return the authentic server build string for this title.
	BuildName = "branch:origin/release/ngs/3.9.x.200x build:3_9_19_2005_0"

	// * The exact library version is derived from every known FAST Racing NEO
	// * RPX revision in kinnay's Wii U library index. FAST links nex, nexrk,
	// * nexmm and nexut 3.9.1, so structures use version/length headers and the
	// * Utility protocol must be registered alongside matchmaking and ranking.
	NEXVersionMajor = 3
	NEXVersionMinor = 9
	NEXVersionPatch = 1
)

// Config holds every runtime-tunable setting, all sourced from the environment.
type Config struct {
	// AuthenticationServerPort is the UDP port the ticket granting server binds.
	AuthenticationServerPort int
	// SecureServerPort is the UDP port the secure (game) server binds.
	SecureServerPort int

	// SecureServerHost is the address handed to the console inside the
	// TicketGranting::Login response so it knows where to find the secure
	// server. This MUST be an address the console can actually reach - a LAN
	// IP for LAN play, or a public IP/hostname for internet play. "localhost"
	// only works if the client runs on this same machine.
	SecureServerHost string

	// AccessKey is the game's 8 character hex access key. Every PRUDP packet
	// signature is derived from it, so a wrong value means the console's
	// packets are silently rejected.
	AccessKey string

	// KerberosPassword is the password for the two special server accounts
	// ("Quazal Authentication" / "Quazal Rendez-Vous"). It never leaves the
	// server, so its value is arbitrary, but it must be identical between the
	// authentication and secure servers.
	KerberosPassword string

	// AccountsFile optionally points at a JSON file mapping NEX PIDs to their
	// Kerberos passwords. See globals/accounts.go.
	AccountsFile string

	// AccountSecret seeds the deterministic password derivation used when a PID
	// is not listed in AccountsFile. An account server paired with this game
	// server can use the same secret to hand consoles a matching password.
	AccountSecret string

	// AllowUnknownAccounts lets any PID authenticate using the derived
	// password. With it disabled only PIDs in AccountsFile may connect.
	AllowUnknownAccounts bool

	// DataDir is where persistent state (leaderboards, common data) is stored.
	DataDir string

	// AllowPublicMatchmaking exposes sessions to BrowseMatchmakeSession and the
	// automatic matchmaker. Disabling it restricts play to friend/invite flows.
	AllowPublicMatchmaking bool

	// LogPackets prints every inbound RMC protocol/method pair. Extremely
	// noisy, but the fastest way to learn what the game actually calls.
	LogPackets bool

	// StructureHeader controls whether NEX structures carry a version/length
	// header: "auto" (detect from the console's first login), "on", or "off".
	// See nexserver/structure_header.go for why this cannot simply be derived
	// from the NEX version.
	StructureHeader string

	// StatsPort is the loopback-only HTTP port that exposes live player counts
	// for the live.protarium.lol status page. 0 disables the stats server.
	StatsPort int
}

// Settings is the process-wide configuration, populated by LoadConfig.
var Settings Config

// LoadConfig reads configuration from the environment, applying defaults.
func LoadConfig() {
	Settings = Config{
		AuthenticationServerPort: envInt("FAST_AUTHENTICATION_SERVER_PORT", 26500),
		SecureServerPort:         envInt("FAST_SECURE_SERVER_PORT", 26501),
		SecureServerHost:         envString("FAST_SECURE_SERVER_HOST", "127.0.0.1"),
		AccessKey:                envString("FAST_ACCESS_KEY", DefaultAccessKey),
		KerberosPassword:         envString("FAST_KERBEROS_PASSWORD", "fast-racing-neo"),
		AccountsFile:             envString("FAST_ACCOUNTS_FILE", ""),
		AccountSecret:            envString("FAST_ACCOUNT_SECRET", ""),
		AllowUnknownAccounts:     envBool("FAST_ALLOW_UNKNOWN_ACCOUNTS", true),
		DataDir:                  envString("FAST_DATA_DIR", "data"),
		AllowPublicMatchmaking:   envBool("FAST_ALLOW_PUBLIC_MATCHMAKING", true),
		LogPackets:               envBool("FAST_LOG_PACKETS", false),
		StructureHeader:          strings.ToLower(envString("FAST_STRUCTURE_HEADER", "on")),
		StatsPort:                envInt("FAST_STATS_PORT", 26502),
	}
}

func envString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

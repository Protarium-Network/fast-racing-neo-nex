package nexserver

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"

	"github.com/ProtariumNetwork/fast-racing-neo/globals"
)

// statsResponse is the JSON shape read by the live.protarium.lol status page.
type statsResponse struct {
	PlayerCount int      `json:"playerCount"`
	PIDs        []uint32 `json:"pids"`
}

// StartStatsServer serves a loopback-only GET /stats with the current
// participant count and PIDs across every live gathering. Matchmaking must
// already be assigned (i.e. call this after StartSecureServer has set it up)
// since a nil Store simply reports zero players rather than panicking.
func StartStatsServer() {
	if globals.Settings.StatsPort <= 0 {
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		pids := make([]uint32, 0)
		if Matchmaking != nil {
			Matchmaking.RLock()
			for _, session := range Matchmaking.AllLocked() {
				for _, participant := range session.Participants {
					pids = append(pids, uint32(participant))
				}
			}
			Matchmaking.RUnlock()
		}

		json.NewEncoder(w).Encode(statsResponse{
			PlayerCount: len(pids),
			PIDs:        pids,
		})
	})

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(globals.Settings.StatsPort))

	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			globals.Logger.Warningf("Stats server on %s stopped: %v", addr, err)
		}
	}()
}

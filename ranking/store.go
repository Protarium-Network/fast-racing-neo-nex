// Package ranking implements persistent leaderboards and per-user common data
// for the Ranking protocol (0x70).
//
// FAST Racing NEO uses the Ranking protocol for persistent leaderboards and
// per-user common data. The JSON store keeps those values across restarts.
package ranking

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/PretendoNetwork/nex-go/v2/types"
	rankingconstants "github.com/PretendoNetwork/nex-protocols-go/v2/ranking/constants"
	rankingtypes "github.com/PretendoNetwork/nex-protocols-go/v2/ranking/types"
)

// Entry is one player's score in one category.
type Entry struct {
	PID      uint64 `json:"pid"`
	UniqueID uint64 `json:"uniqueId"`
	Category uint32 `json:"category"`
	Score    uint32 `json:"score"`

	// OrderBy records how the game wants this category ranked, taken from the
	// score upload. 0 sorts ascending (lowest first, which is what a lap time
	// leaderboard wants); 1 sorts descending.
	OrderBy uint8 `json:"orderBy"`

	// Groups is the game-defined grouping blob, used to filter a leaderboard
	// (by track, by vehicle class, and so on).
	Groups []byte `json:"groups,omitempty"`

	// Param is a game-defined value carried alongside the score. Racing games
	// typically use it for a ghost data reference or a replay identifier.
	Param uint64 `json:"param"`

	// UpdateTime is when the score was last accepted, as a NEX DateTime.
	UpdateTime uint64 `json:"updateTime"`
}

// persistedState is the on-disk format.
type persistedState struct {
	// Scores are keyed by category, then by PID.
	Scores map[string]map[string]*Entry `json:"scores"`

	// CommonData is keyed by unique ID.
	CommonData map[string][]byte `json:"commonData"`
}

// Store holds leaderboards and common data.
type Store struct {
	mutex sync.RWMutex

	// scores maps category -> PID -> entry. One score per player per category,
	// which is how NEX leaderboards work: a new upload replaces the old entry
	// rather than adding to it.
	scores map[uint32]map[uint64]*Entry

	// commonData maps unique ID -> blob.
	commonData map[uint64][]byte

	path string

	// GetUserFriendPIDs resolves a friend list for the friends-only ranking
	// modes. Without a friends server it returns nothing, so those modes
	// correctly report no results rather than leaking the global leaderboard.
	GetUserFriendPIDs func(pid uint32) []uint32
}

// NewStore returns a Store persisting to dataDir/ranking.json, loading any
// existing contents.
func NewStore(dataDir string) (*Store, error) {
	store := &Store{
		scores:     make(map[uint32]map[uint64]*Entry),
		commonData: make(map[uint64][]byte),
		path:       filepath.Join(dataDir, "ranking.json"),
		GetUserFriendPIDs: func(pid uint32) []uint32 {
			return []uint32{}
		},
	}

	if err := store.load(); err != nil {
		return nil, err
	}

	return store, nil
}

func (s *Store) load() error {
	contents, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return fmt.Errorf("failed to read %s: %w", s.path, err)
	}

	var state persistedState
	if err := json.Unmarshal(contents, &state); err != nil {
		return fmt.Errorf("failed to parse %s: %w", s.path, err)
	}

	for categoryKey, entries := range state.Scores {
		var category uint32
		if _, err := fmt.Sscanf(categoryKey, "%d", &category); err != nil {
			continue
		}

		s.scores[category] = make(map[uint64]*Entry, len(entries))

		for pidKey, entry := range entries {
			var pid uint64
			if _, err := fmt.Sscanf(pidKey, "%d", &pid); err != nil {
				continue
			}

			s.scores[category][pid] = entry
		}
	}

	for uniqueIDKey, blob := range state.CommonData {
		var uniqueID uint64
		if _, err := fmt.Sscanf(uniqueIDKey, "%d", &uniqueID); err != nil {
			continue
		}

		s.commonData[uniqueID] = blob
	}

	return nil
}

// save writes the store to disk. The caller must hold at least the read lock.
//
// The write goes to a temporary file first and is then renamed over the target,
// so a crash mid-write cannot leave a truncated leaderboard behind.
func (s *Store) save() error {
	state := persistedState{
		Scores:     make(map[string]map[string]*Entry, len(s.scores)),
		CommonData: make(map[string][]byte, len(s.commonData)),
	}

	for category, entries := range s.scores {
		key := fmt.Sprintf("%d", category)
		state.Scores[key] = make(map[string]*Entry, len(entries))

		for pid, entry := range entries {
			state.Scores[key][fmt.Sprintf("%d", pid)] = entry
		}
	}

	for uniqueID, blob := range s.commonData {
		state.CommonData[fmt.Sprintf("%d", uniqueID)] = blob
	}

	contents, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode ranking state: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", filepath.Dir(s.path), err)
	}

	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, contents, 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", temporary, err)
	}

	if err := os.Rename(temporary, s.path); err != nil {
		return fmt.Errorf("failed to replace %s: %w", s.path, err)
	}

	return nil
}

// * ---------------------------------------------------------------------------
// * Score storage
// * ---------------------------------------------------------------------------

// InsertRankingByPIDAndRankingScoreData records a score upload.
//
// UpdateMode decides what happens when the player already has a score in this
// category. NEX does not document the enum, so this server takes the reading
// that makes a leaderboard behave the way players expect:
//
//	UpdateModeNormal (0)  keep whichever score is better, per OrderBy
//	anything else         overwrite unconditionally
//
// Keeping the better score is the safe default: a player who posts a worse lap
// time on a later run does not lose their record.
func (s *Store) InsertRankingByPIDAndRankingScoreData(pid types.PID, scoreData rankingtypes.RankingScoreData, uniqueID types.UInt64) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	category := uint32(scoreData.Category)

	if _, ok := s.scores[category]; !ok {
		s.scores[category] = make(map[uint64]*Entry)
	}

	candidate := &Entry{
		PID:        uint64(pid),
		UniqueID:   uint64(uniqueID),
		Category:   category,
		Score:      uint32(scoreData.Score),
		OrderBy:    uint8(scoreData.OrderBy),
		Groups:     append([]byte(nil), scoreData.Groups...),
		Param:      uint64(scoreData.Param),
		UpdateTime: uint64(types.NewDateTime(0).Now()),
	}

	existing, hasExisting := s.scores[category][uint64(pid)]

	if hasExisting && scoreData.UpdateMode == rankingconstants.UpdateModeNormal {
		if !betterThan(candidate.Score, existing.Score, candidate.OrderBy) {
			// * The existing score stands, but the upload is not an error.
			return nil
		}
	}

	s.scores[category][uint64(pid)] = candidate

	return s.save()
}

// betterThan reports whether score beats other under the given ordering.
func betterThan(score uint32, other uint32, orderBy uint8) bool {
	if rankingconstants.OrderBy(orderBy) == rankingconstants.OrderByDescending {
		return score > other
	}

	// * Ascending: a lower value is better, which is what lap times need.
	return score < other
}

// DeleteScore removes a player's score in one category.
func (s *Store) DeleteScore(pid uint64, category uint32) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if entries, ok := s.scores[category]; ok {
		delete(entries, pid)

		if len(entries) == 0 {
			delete(s.scores, category)
		}
	}

	return s.save()
}

// DeleteAllScores removes every score a player holds.
func (s *Store) DeleteAllScores(pid uint64) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for category, entries := range s.scores {
		delete(entries, pid)

		if len(entries) == 0 {
			delete(s.scores, category)
		}
	}

	return s.save()
}

// * ---------------------------------------------------------------------------
// * Common data
// * ---------------------------------------------------------------------------

// GetCommonData returns the blob stored for a unique ID.
func (s *Store) GetCommonData(uniqueID types.UInt64) (types.Buffer, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	blob, ok := s.commonData[uint64(uniqueID)]
	if !ok {
		return types.NewBuffer(nil), fmt.Errorf("no common data for unique ID %d", uint64(uniqueID))
	}

	return types.NewBuffer(append([]byte(nil), blob...)), nil
}

// UploadCommonData stores a blob against a unique ID.
func (s *Store) UploadCommonData(pid types.PID, uniqueID types.UInt64, commonData types.Buffer) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.commonData[uint64(uniqueID)] = append([]byte(nil), commonData...)

	return s.save()
}

// DeleteCommonData removes the blob for a unique ID.
func (s *Store) DeleteCommonData(uniqueID uint64) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	delete(s.commonData, uniqueID)

	return s.save()
}

// * ---------------------------------------------------------------------------
// * Leaderboard queries
// * ---------------------------------------------------------------------------

// rankedEntries returns a category's entries in rank order.
// The caller must hold at least the read lock.
func (s *Store) rankedEntries(category uint32) []*Entry {
	entries := make([]*Entry, 0, len(s.scores[category]))
	for _, entry := range s.scores[category] {
		entries = append(entries, entry)
	}

	// * Every entry in a category shares an ordering in practice, since the
	// * game uploads a consistent OrderBy. Take it from the first entry.
	orderBy := uint8(0)
	if len(entries) > 0 {
		orderBy = entries[0].OrderBy
	}

	sort.SliceStable(entries, func(a, b int) bool {
		if entries[a].Score != entries[b].Score {
			return betterThan(entries[a].Score, entries[b].Score, orderBy)
		}

		// * Tie break on who got there first, so ranks are stable.
		return entries[a].UpdateTime < entries[b].UpdateTime
	})

	return entries
}

// rankData converts entries to RankingRankData, assigning ranks.
//
// ranks are 1-based and computed over the FULL ordered list before slicing, so
// a player at offset 50 correctly reports rank 51 rather than rank 1.
func (s *Store) rankData(ordered []*Entry, selected []*Entry, orderCalculation rankingconstants.OrderCalculation) types.List[rankingtypes.RankingRankData] {
	ranks := make(map[uint64]uint32, len(ordered))

	// * OrderCalculation 0 is standard competition ranking (1,2,2,4);
	// * 1 is ordinal ranking (1,2,3,4).
	standard := orderCalculation == rankingconstants.OrderCalculation113

	var previousScore uint32
	var previousRank uint32

	for index, entry := range ordered {
		rank := uint32(index) + 1

		if standard && index > 0 && entry.Score == previousScore {
			rank = previousRank
		}

		ranks[entry.PID] = rank
		previousScore = entry.Score
		previousRank = rank
	}

	list := types.NewList[rankingtypes.RankingRankData]()

	for _, entry := range selected {
		data := rankingtypes.NewRankingRankData()
		data.PrincipalID = types.NewPID(entry.PID)
		data.UniqueID = types.NewUInt64(entry.UniqueID)
		data.Order = types.NewUInt32(ranks[entry.PID])
		data.Category = types.NewUInt32(entry.Category)
		data.Score = types.NewUInt32(entry.Score)
		data.Groups = types.NewBuffer(append([]byte(nil), entry.Groups...))
		data.Param = types.NewUInt64(entry.Param)
		data.CommonData = types.NewBuffer(append([]byte(nil), s.commonData[entry.UniqueID]...))
		data.UpdateTime = types.NewDateTime(entry.UpdateTime)

		list = append(list, data)
	}

	return list
}

// slice applies a RankingOrderParam's offset and length.
func slice(entries []*Entry, orderParam rankingtypes.RankingOrderParam) []*Entry {
	offset := int(orderParam.Offset)
	length := int(orderParam.Length)

	if offset >= len(entries) {
		return nil
	}

	selected := entries[offset:]

	if length > 0 && length < len(selected) {
		selected = selected[:length]
	}

	return selected
}

// GetRankingsAndCountByCategoryAndRankingOrderParam serves RankingMode 0, the
// global leaderboard.
func (s *Store) GetRankingsAndCountByCategoryAndRankingOrderParam(category types.UInt32, orderParam rankingtypes.RankingOrderParam) (types.List[rankingtypes.RankingRankData], uint32, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	ordered := s.rankedEntries(uint32(category))
	selected := slice(ordered, orderParam)

	return s.rankData(ordered, selected, orderParam.OrderCalculation), uint32(len(ordered)), nil
}

// GetNearbyRankingsAndCountByCategoryAndRankingOrderParam serves RankingMode 1,
// the entries surrounding the caller.
func (s *Store) GetNearbyRankingsAndCountByCategoryAndRankingOrderParam(pid types.PID, category types.UInt32, orderParam rankingtypes.RankingOrderParam) (types.List[rankingtypes.RankingRankData], uint32, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	ordered := s.rankedEntries(uint32(category))

	// * Centre the window on the caller. Offset is relative to their position
	// * rather than to the top of the board for this mode.
	position := -1
	for index, entry := range ordered {
		if entry.PID == uint64(pid) {
			position = index
			break
		}
	}

	if position < 0 {
		return types.NewList[rankingtypes.RankingRankData](), uint32(len(ordered)), nil
	}

	length := int(orderParam.Length)
	if length <= 0 {
		length = 1
	}

	start := position - length/2
	if start < 0 {
		start = 0
	}

	end := start + length
	if end > len(ordered) {
		end = len(ordered)
	}

	return s.rankData(ordered, ordered[start:end], orderParam.OrderCalculation), uint32(len(ordered)), nil
}

// GetFriendsRankingsAndCountByCategoryAndRankingOrderParam serves RankingMode 2.
func (s *Store) GetFriendsRankingsAndCountByCategoryAndRankingOrderParam(pid types.PID, category types.UInt32, orderParam rankingtypes.RankingOrderParam) (types.List[rankingtypes.RankingRankData], uint32, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	friends := s.friendSet(pid)
	ordered := filterEntries(s.rankedEntries(uint32(category)), friends)
	selected := slice(ordered, orderParam)

	return s.rankData(ordered, selected, orderParam.OrderCalculation), uint32(len(ordered)), nil
}

// GetNearbyFriendsRankingsAndCountByCategoryAndRankingOrderParam serves
// RankingMode 3.
func (s *Store) GetNearbyFriendsRankingsAndCountByCategoryAndRankingOrderParam(pid types.PID, category types.UInt32, orderParam rankingtypes.RankingOrderParam) (types.List[rankingtypes.RankingRankData], uint32, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	friends := s.friendSet(pid)
	ordered := filterEntries(s.rankedEntries(uint32(category)), friends)
	selected := slice(ordered, orderParam)

	return s.rankData(ordered, selected, orderParam.OrderCalculation), uint32(len(ordered)), nil
}

// GetOwnRankingByCategoryAndRankingOrderParam serves RankingMode 4.
func (s *Store) GetOwnRankingByCategoryAndRankingOrderParam(pid types.PID, category types.UInt32, orderParam rankingtypes.RankingOrderParam) (types.List[rankingtypes.RankingRankData], uint32, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	ordered := s.rankedEntries(uint32(category))

	var own []*Entry
	for _, entry := range ordered {
		if entry.PID == uint64(pid) {
			own = append(own, entry)
			break
		}
	}

	return s.rankData(ordered, own, orderParam.OrderCalculation), uint32(len(ordered)), nil
}

// RankingsForPIDs returns the ranks of a specific set of players, used by
// GetRankingByPIDList.
func (s *Store) RankingsForPIDs(pids []uint64, category uint32, orderParam rankingtypes.RankingOrderParam) (types.List[rankingtypes.RankingRankData], uint32) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	wanted := make(map[uint64]bool, len(pids))
	for _, pid := range pids {
		wanted[pid] = true
	}

	ordered := s.rankedEntries(category)
	selected := filterEntries(ordered, wanted)

	return s.rankData(ordered, selected, orderParam.OrderCalculation), uint32(len(ordered))
}

// ApproximateOrder returns the rank a score would take in a category without
// storing it, which is what a game shows as a projected position at the end of
// a race before the player commits the time.
func (s *Store) ApproximateOrder(category uint32, score uint32) uint32 {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	ordered := s.rankedEntries(category)

	orderBy := uint8(0)
	if len(ordered) > 0 {
		orderBy = ordered[0].OrderBy
	}

	// * The rank is one past however many existing scores beat this one.
	order := uint32(1)
	for _, entry := range ordered {
		if betterThan(entry.Score, score, orderBy) {
			order++
		}
	}

	return order
}

// friendSet returns a PID's friends as a lookup set, always including the PID
// itself so a player can see their own entry on a friends board.
func (s *Store) friendSet(pid types.PID) map[uint64]bool {
	set := map[uint64]bool{uint64(pid): true}

	if s.GetUserFriendPIDs == nil {
		return set
	}

	for _, friend := range s.GetUserFriendPIDs(uint32(pid)) {
		set[uint64(friend)] = true
	}

	return set
}

func filterEntries(entries []*Entry, allowed map[uint64]bool) []*Entry {
	filtered := make([]*Entry, 0, len(entries))
	for _, entry := range entries {
		if allowed[entry.PID] {
			filtered = append(filtered, entry)
		}
	}

	return filtered
}

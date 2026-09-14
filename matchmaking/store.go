// Package matchmaking implements an in-memory gathering store and the three
// matchmaking protocols a NEX 3.0.1 title can speak.
//
// Pretendo's nex-protocols-common-go backs matchmaking with PostgreSQL. That is
// the right call for a service hosting thousands of concurrent sessions, but a
// gathering is inherently ephemeral state - every session dies when the server
// restarts regardless of where it was stored - so this server keeps them in a
// map instead. The observable behaviour is modelled directly on common-go's
// implementation, including the participation notification events, which is the
// part the game actually depends on to bring a race lobby up.
package matchmaking

import (
	"crypto/rand"
	"sync"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	commonglobals "github.com/PretendoNetwork/nex-protocols-common-go/v2/globals"
	matchmakingconstants "github.com/PretendoNetwork/nex-protocols-go/v2/match-making/constants"
	matchmakingtypes "github.com/PretendoNetwork/nex-protocols-go/v2/match-making/types"
	notificationsconstants "github.com/PretendoNetwork/nex-protocols-go/v2/notifications/constants"
	notificationstypes "github.com/PretendoNetwork/nex-protocols-go/v2/notifications/types"
)

// Session is one live gathering plus the bookkeeping the protocols need.
type Session struct {
	// MatchmakeSession is the session as the game described it. Its embedded
	// Gathering carries ID, OwnerPID, HostPID, participant limits and state.
	MatchmakeSession matchmakingtypes.MatchmakeSession

	// Participants holds the PID of every participant. A PID appears more than
	// once when a console joins with additional local players, which is how NEX
	// represents split-screen guests - so len(Participants) is the true head
	// count, not the number of distinct consoles.
	Participants []uint64

	// StartedTime is when the session was registered.
	StartedTime types.DateTime

	// SessionURL is the host's session URL, set by MatchMaking::LaunchSession.
	SessionURL types.String
}

// ParticipationCount returns the number of participants in the session.
func (s *Session) ParticipationCount() uint32 {
	return uint32(len(s.Participants))
}

// Contains reports whether the PID is a participant.
func (s *Session) Contains(pid uint64) bool {
	for _, participant := range s.Participants {
		if participant == pid {
			return true
		}
	}

	return false
}

// Store holds every live gathering.
type Store struct {
	mutex    sync.RWMutex
	sessions map[uint32]*Session

	// nextGatheringID is the ID handed to the next registered gathering.
	// Gathering IDs must be non-zero; the game treats 0 as "no gathering".
	nextGatheringID uint32

	// notificationData holds the last notification each user published through
	// MatchmakeExtension::UpdateNotificationData, keyed by PID then by type.
	// It is what friends read back via GetFriendNotificationData.
	notificationData map[uint64]map[uint32]notificationstypes.NotificationEvent

	// Endpoint is the secure endpoint, needed to push notification events and
	// to resolve a participant's station URLs.
	Endpoint *nex.PRUDPEndPoint

	// GetUserFriendPIDs resolves a user's friend list. Friend data lives on the
	// separate Wii U friends server, so without one this returns an empty list
	// and friends-only sessions simply cannot be joined by strangers.
	GetUserFriendPIDs func(pid uint32) []uint32
}

// NewStore returns an empty Store bound to the given endpoint.
func NewStore(endpoint *nex.PRUDPEndPoint) *Store {
	return &Store{
		sessions:         make(map[uint32]*Session),
		notificationData: make(map[uint64]map[uint32]notificationstypes.NotificationEvent),
		nextGatheringID:  1,
		Endpoint:         endpoint,
		GetUserFriendPIDs: func(pid uint32) []uint32 {
			return []uint32{}
		},
	}
}

// Lock and Unlock expose the store mutex so a protocol handler can perform a
// find-then-join sequence atomically.
func (s *Store) Lock()    { s.mutex.Lock() }
func (s *Store) Unlock()  { s.mutex.Unlock() }
func (s *Store) RLock()   { s.mutex.RLock() }
func (s *Store) RUnlock() { s.mutex.RUnlock() }

// GetLocked returns the session with the given ID. The caller must hold the
// lock.
func (s *Store) GetLocked(gatheringID uint32) (*Session, *nex.Error) {
	session, ok := s.sessions[gatheringID]
	if !ok {
		return nil, nex.NewError(nex.ResultCodes.RendezVous.SessionVoid, "Gathering does not exist")
	}

	return session, nil
}

// Get returns the session with the given ID, taking the read lock.
func (s *Store) Get(gatheringID uint32) (*Session, *nex.Error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	return s.GetLocked(gatheringID)
}

// AllLocked returns every live session. The caller must hold the lock.
func (s *Store) AllLocked() []*Session {
	sessions := make([]*Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session)
	}

	return sessions
}

// CreateLocked registers a new gathering owned and hosted by the connection.
// The owner is NOT added as a participant here - the caller joins them, exactly
// as common-go does, so that a single code path handles the participant count
// and the notification suppression for the owner's own join.
//
// The caller must hold the write lock.
func (s *Store) CreateLocked(connection *nex.PRUDPConnection, session matchmakingtypes.MatchmakeSession) *Session {
	gatheringID := s.nextGatheringID
	s.nextGatheringID++

	session.ID = types.NewUInt32(gatheringID)
	session.OwnerPID = connection.PID()
	session.HostPID = connection.PID()
	session.StartedTime = types.NewDateTime(0).Now()

	// * The session key is what the participants use to secure their peer to
	// * peer traffic once the lobby is up. NEX generates 32 random bytes and
	// * hands the same value to everyone who joins.
	sessionKey := make([]byte, 32)
	if _, err := rand.Read(sessionKey); err != nil {
		// * crypto/rand failing is not recoverable, but a zero key is worse
		// * than a weak one only if it is silently accepted, so log loudly.
		commonglobals.Logger.Errorf("Failed to generate a session key for gathering %d: %v", gatheringID, err)
	}

	session.SessionKey = types.NewBuffer(sessionKey)
	session.UserPasswordEnabled = types.NewBool(len(session.UserPassword) > 0)
	session.SystemPasswordEnabled = types.NewBool(false)

	stored := &Session{
		MatchmakeSession: session,
		Participants:     make([]uint64, 0, session.MaximumParticipants),
		StartedTime:      types.NewDateTime(0).Now(),
	}

	s.sessions[gatheringID] = stored

	return stored
}

// JoinLocked adds a connection to a gathering and notifies the other
// participants. vacantParticipants is the total number of players joining,
// including the primary one.
//
// The caller must hold the write lock.
func (s *Store) JoinLocked(session *Session, connection *nex.PRUDPConnection, vacantParticipants uint16, joinMessage string) (uint32, *nex.Error) {
	if vacantParticipants == 0 {
		vacantParticipants = 1
	}

	pid := uint64(connection.PID())

	if session.Contains(pid) {
		return 0, nex.NewError(nex.ResultCodes.RendezVous.AlreadyParticipatedGathering, "Already a participant of this gathering")
	}

	maximumParticipants := uint32(session.MatchmakeSession.MaximumParticipants)
	if maximumParticipants != 0 && session.ParticipationCount()+uint32(vacantParticipants) > maximumParticipants {
		return 0, nex.NewError(nex.ResultCodes.RendezVous.SessionFull, "Gathering is full")
	}

	previousParticipants := make([]uint64, len(session.Participants))
	copy(previousParticipants, session.Participants)

	for i := uint16(0); i < vacantParticipants; i++ {
		session.Participants = append(session.Participants, pid)
	}

	total := session.ParticipationCount()
	session.MatchmakeSession.ParticipationCount = types.NewUInt32(total)

	gatheringID := uint32(session.MatchmakeSession.ID)
	flags := session.MatchmakeSession.Flags

	var targets []uint64

	if flags.HasFlag(matchmakingconstants.GatheringFlagNotifyParticipationEventsToAllParticipants) ||
		flags.HasFlag(matchmakingconstants.GatheringFlagNotifyParticipationEventsToAllParticipantsReproducibly) {
		targets = commonglobals.RemoveDuplicates(session.Participants)
	} else {
		// * The owner creating their own gathering is not an event anyone
		// * needs to hear about.
		if pid == uint64(session.MatchmakeSession.OwnerPID) {
			return total, nil
		}

		targets = []uint64{uint64(session.MatchmakeSession.OwnerPID)}
	}

	s.notifyParticipation(
		connection.PID(),
		notificationsconstants.ParticipationEventsParticipate,
		gatheringID,
		pid,
		joinMessage,
		uint64(total),
		targets,
	)

	// * This flag additionally replays the existing participants to the player
	// * who just joined, so their lobby list starts out complete.
	if flags.HasFlag(matchmakingconstants.GatheringFlagNotifyParticipationEventsToAllParticipantsReproducibly) {
		for _, participant := range commonglobals.RemoveDuplicates(previousParticipants) {
			s.notifyParticipation(
				connection.PID(),
				notificationsconstants.ParticipationEventsParticipate,
				gatheringID,
				participant,
				joinMessage,
				uint64(total),
				[]uint64{pid},
			)
		}
	}

	return total, nil
}

// LeaveLocked removes every instance of a PID from a gathering and notifies the
// remaining participants.
//
// If the leaver was the owner the gathering is destroyed, matching NEX's
// behaviour: an owner leaving ends the session for everyone. If the leaver was
// merely the host, hosting migrates to the owner.
//
// The caller must hold the write lock.
func (s *Store) LeaveLocked(session *Session, pid uint64, subType notificationsconstants.ParticipationEvents, message string) {
	if !session.Contains(pid) {
		return
	}

	remaining := make([]uint64, 0, len(session.Participants))
	for _, participant := range session.Participants {
		if participant != pid {
			remaining = append(remaining, participant)
		}
	}

	session.Participants = remaining
	session.MatchmakeSession.ParticipationCount = types.NewUInt32(session.ParticipationCount())

	gatheringID := uint32(session.MatchmakeSession.ID)

	s.notifyParticipation(
		types.NewPID(pid),
		subType,
		gatheringID,
		pid,
		message,
		uint64(session.ParticipationCount()),
		commonglobals.RemoveDuplicates(session.Participants),
	)

	if uint64(session.MatchmakeSession.OwnerPID) == pid || len(session.Participants) == 0 {
		delete(s.sessions, gatheringID)
		return
	}

	if uint64(session.MatchmakeSession.HostPID) == pid {
		session.MatchmakeSession.HostPID = session.MatchmakeSession.OwnerPID
	}
}

// RemoveConnection drops a connection from every gathering it participates in.
// Called when a console disconnects, gracefully or otherwise, and before an
// automatic matchmake so a stale membership cannot block a new one.
func (s *Store) RemoveConnection(pid uint64, subType notificationsconstants.ParticipationEvents) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.RemoveConnectionLocked(pid, subType)
}

// RemoveConnectionLocked is RemoveConnection without taking the lock.
func (s *Store) RemoveConnectionLocked(pid uint64, subType notificationsconstants.ParticipationEvents) {
	for _, session := range s.AllLocked() {
		if session.Contains(pid) {
			s.LeaveLocked(session, pid, subType, "")
		}
	}
}

// notifyParticipation pushes a ParticipationEvent notification to targets.
//
// At NEX 3.0.1 NotificationEvent serialises as PIDSource, Type, Param1, Param2,
// StrParam - Param3 is gated behind 3.4.0 and is simply not written, so setting
// it here is harmless and keeps the code honest about intent.
func (s *Store) notifyParticipation(source types.PID, subType notificationsconstants.ParticipationEvents, gatheringID uint32, subjectPID uint64, message string, participantCount uint64, targets []uint64) {
	if s.Endpoint == nil || len(targets) == 0 {
		return
	}

	event := notificationstypes.NewNotificationEvent()
	event.PIDSource = source
	event.Type = notificationsconstants.NotificationCategoryParticipationEvent.Build(subType)
	event.Param1 = types.UInt64(gatheringID)
	event.Param2 = types.UInt64(subjectPID)
	event.StrParam = types.NewString(message)
	event.Param3 = types.UInt64(participantCount)

	commonglobals.SendNotificationEvent(s.Endpoint, event, targets)
}

// SetNotificationData records a user's published notification data.
func (s *Store) SetNotificationData(pid uint64, event notificationstypes.NotificationEvent) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if _, ok := s.notificationData[pid]; !ok {
		s.notificationData[pid] = make(map[uint32]notificationstypes.NotificationEvent)
	}

	s.notificationData[pid][uint32(event.Type)] = event
}

// NotificationDataForPIDs returns every stored notification of the given types
// published by any of the given PIDs.
func (s *Store) NotificationDataForPIDs(pids []uint64, categories []uint32) []notificationstypes.NotificationEvent {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	events := make([]notificationstypes.NotificationEvent, 0)

	for _, pid := range pids {
		stored, ok := s.notificationData[pid]
		if !ok {
			continue
		}

		for _, category := range categories {
			for eventType, event := range stored {
				// * Stored types are fully built (category*1000 + subtype),
				// * while a lookup is by category, so compare on the category.
				if eventType/1000 == category || eventType == category {
					events = append(events, event)
				}
			}
		}
	}

	return events
}

// FindByParticipantLocked returns every session the PID participates in.
// The caller must hold the lock.
func (s *Store) FindByParticipantLocked(pid uint64) []*Session {
	found := make([]*Session, 0)

	for _, session := range s.AllLocked() {
		if session.Contains(pid) {
			found = append(found, session)
		}
	}

	return found
}

// FindByOwnerLocked returns every session owned by the PID.
// The caller must hold the lock.
func (s *Store) FindByOwnerLocked(pid uint64) []*Session {
	found := make([]*Session, 0)

	for _, session := range s.AllLocked() {
		if uint64(session.MatchmakeSession.OwnerPID) == pid {
			found = append(found, session)
		}
	}

	return found
}

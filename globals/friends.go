package globals

// GetUserFriendPIDs returns a user's friend list.
//
// On a Wii U, friends do not live on the game server at all - they live on the
// separate system-wide friends server (access key "ridfebb9", NEX 1.0.0), which
// every title queries independently. This server has no connection to one, so
// it reports that nobody has friends.
//
// The consequences are contained and worth stating plainly:
//
//   - Friends-only gatherings (ParticipationPolicy 98) cannot be joined by
//     anyone except the owner, because no one can be proven to be a friend.
//   - The friends-scoped ranking modes return empty leaderboards rather than
//     falling back to global ones, which is the safe direction to fail.
//   - Public matchmaking, direct joins by gathering ID, and global leaderboards
//     are unaffected.
//
// To wire in a real friends server, replace this with a lookup against it; both
// the matchmaking store and the ranking store take it as a function field, so
// nothing else has to change.
func GetUserFriendPIDs(pid uint32) []uint32 {
	return []uint32{}
}

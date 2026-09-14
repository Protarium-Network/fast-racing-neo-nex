"""End-to-end test of the FAST Racing NEO NEX server.

Drives the server with kinnay's NintendoClients - the same library used to talk
to the real Nintendo servers - so what is exercised is the actual PRUDP/RMC wire
protocol, not a mock.

Covers: Kerberos login through the auth server, the handoff to the secure
server, station URL registration, session create/browse/join, participant and
peer URL lookup, host migration, leaderboards, and leaving.
"""

import hashlib
import hmac
import os
import sys
import traceback

import anyio

from nintendo.nex import authentication, backend, common, matchmaking, ranking, secure, settings


def auth_info():
    """The extra login data a Wii U sends with TicketGranting::LoginEx.

    Passing this makes NintendoClients call LoginEx (0x2) rather than the
    legacy Login (0x1), which is what a real console does: the token is the
    NEX token the account server issued for game server 0x1012F000.
    """
    info = authentication.AuthenticationInfo()
    info.token = "e2e-test-token"
    info.ngs_version = 3      # * Wii U
    info.token_type = 1
    info.server_version = 3
    return info

HOST = "127.0.0.1"
AUTH_PORT = 26500
ACCESS_KEY = "811aa39f"
NEX_VERSION = 30901  # * 3.9.1

ACCOUNT_SECRET = "00" * 32
PID_A = 1800000001
PID_B = 1800000002

failures = []
passes = []


def check(name, condition, detail=""):
    if condition:
        passes.append(name)
        print(f"  PASS  {name}" + (f"  ({detail})" if detail else ""))
    else:
        failures.append(name)
        print(f"  FAIL  {name}" + (f"  ({detail})" if detail else ""))


def derive_password(pid):
    """Mirror of globals.DerivePassword in the Go server."""
    import base64

    secret = bytes.fromhex(ACCOUNT_SECRET)
    mac = hmac.new(secret, pid.to_bytes(8, "little"), hashlib.sha256).digest()
    return base64.urlsafe_b64encode(mac).decode().rstrip("=")


def make_settings():
    s = settings.default()
    s.configure(ACCESS_KEY, NEX_VERSION)

    # Wii U NEX game servers use framed PRUDP (V1), not PRUDP-Lite (V2).
    s["prudp.version"] = 1

    # * The PRUDP minor version the client announces is what actually decides
    # * whether NEX structures carry version/length headers: NintendoClients
    # * turns them on at minor version 3 and above. Running the suite at both 4
    # * and 2 exercises both wire formats, which is the point - nobody knows
    # * which one the real console uses, so the server has to handle either.
    s["prudp.minor_version"] = int(os.environ.get("E2E_MINOR_VERSION", "4"))

    return s


def new_session(game_mode=1, attribs=None, max_participants=8):
    session = matchmaking.MatchmakeSession()
    session.game_mode = game_mode
    session.attribs = attribs if attribs is not None else [1, 0, 0, 0, 0, 0]
    session.open_participation = True
    session.matchmake_system = 1
    session.application_data = b"\x01\x02\x03\x04"
    session.num_participants = 1
    session.min_participants = 2
    session.max_participants = max_participants
    session.participation_policy = 0
    session.policy_argument = 0
    session.flags = 0
    session.state = 0
    session.description = "E2E race"
    session.session_key = b""
    session.progress_score = 0
    session.user_password = ""
    session.refer_gid = 0
    session.user_password_enabled = False
    session.system_password_enabled = False
    session.codeword = ""
    return session


async def register_station(client, label):
    """Publish station URLs, exactly as a console does after connecting.

    This is what makes peer-to-peer play possible: the server records where
    each player can be reached and hands those addresses to the others. The
    local address we claim here is deliberately a LAN one - the server should
    replace the public station's address with the address it actually observes.
    """
    conn = secure.SecureConnectionClient(client)

    local = common.StationURL(
        "prudp", address="192.168.1.50", port=12345, natm=0, natf=0,
        type=1, upnp=0, pmp=0, sid=15,
    )

    result = await conn.register_ex([local], auth_info())
    print(f"        {label} public station: {result.public_station}")
    return result


def search_criteria():
    criteria = matchmaking.MatchmakeSessionSearchCriteria()
    criteria.attribs = ["", "", "", "", "", ""]
    criteria.game_mode = "1"
    criteria.min_participants = ""
    criteria.max_participants = ""
    criteria.matchmake_system = ""
    criteria.vacant_only = False
    criteria.exclude_locked = True
    criteria.exclude_non_host_pid = True
    criteria.selection_method = 0
    criteria.vacant_participants = 1
    criteria.exclude_user_password_set = False
    criteria.exclude_system_password_set = False
    criteria.refer_gid = 0
    criteria.codeword = ""
    return criteria


async def main():
    s = make_settings()

    print("\n=== 1. Authentication (Kerberos via ticket granting) ===")

    async with backend.connect(s, HOST, AUTH_PORT) as be_a:
        async with be_a.login(str(PID_A), derive_password(PID_A), auth_info()) as client_a:
            check("client A logged in and reached the secure server", True, f"pid={PID_A}")
            await register_station(client_a, "A")

            mm_a = matchmaking.MatchmakeExtensionClient(client_a)
            mmext_a = matchmaking.MatchMakingClientExt(client_a)
            mm_proto_a = matchmaking.MatchMakingClient(client_a)
            rank_a = ranking.RankingClient(client_a)

            print("\n=== 2. Session creation (MatchmakeExtension::CreateMatchmakeSession) ===")

            created = await mm_a.create_matchmake_session(new_session(), "hosting", 1)
            gid = created.gid
            session_key = created.session_key
            check("CreateMatchmakeSession returned a gathering ID", gid > 0, f"gid={gid}")
            check("CreateMatchmakeSession returned a 32-byte session key",
                  len(session_key) == 32, f"{len(session_key)} bytes")

            print("\n=== 3. Second player joins ===")

            async with backend.connect(s, HOST, AUTH_PORT) as be_b:
                async with be_b.login(str(PID_B), derive_password(PID_B), auth_info()) as client_b:
                    check("client B logged in", True, f"pid={PID_B}")
                    await register_station(client_b, "B")

                    mm_b = matchmaking.MatchmakeExtensionClient(client_b)
                    mmext_b = matchmaking.MatchMakingClientExt(client_b)
                    rank_b = ranking.RankingClient(client_b)

                    found = await mm_b.browse_matchmake_session(search_criteria(), common.ResultRange(0, 10))
                    check("BrowseMatchmakeSession found the hosted session",
                          any(g.id == gid for g in found), f"{len(found)} session(s)")

                    if found:
                        listed = next(g for g in found if g.id == gid)
                        check("browsed session reports the owner", listed.owner == PID_A, f"owner={listed.owner}")
                        check("browsed session hides the session key",
                              len(listed.session_key) == 0, f"{len(listed.session_key)} bytes")
                        check("browsed session preserves the application buffer",
                              listed.application_data == b"\x01\x02\x03\x04", repr(listed.application_data))

                    join_key = await mm_b.join_matchmake_session(gid, "joining")
                    check("JoinMatchmakeSession returned the shared session key",
                          join_key == session_key, "matches the host's key")

                    print("\n=== 4. Participants and peer-to-peer URLs ===")

                    participants = await mmext_b.get_participants(gid, True)
                    check("MatchMakingExt::GetParticipants lists both players",
                          sorted(participants) == sorted([PID_A, PID_B]), str(participants))

                    details = await mmext_b.get_detailed_participants(gid, True)
                    check("GetDetailedParticipants returns one entry per player",
                          len(details) == 2, f"{len(details)} entries")

                    urls = await mmext_b.get_participants_urls([gid])
                    check("GetParticipantsURLs returns an entry for the gathering",
                          len(urls) == 1 and urls[0].gid == gid, str(len(urls)))
                    if urls:
                        check("GetParticipantsURLs returns station URLs for peer connection",
                              len(urls[0].urls) > 0, f"{len(urls[0].urls)} URL(s)")
                        if urls[0].urls:
                            print(f"        station URL: {urls[0].urls[0]}")

                    session_urls = await mm_proto_a.get_session_urls(gid)
                    check("MatchMaking::GetSessionURLs returns the host's URLs",
                          len(session_urls) > 0, f"{len(session_urls)} URL(s)")

                    print("\n=== 5. Session mutation ===")

                    await mm_a.update_application_buffer(gid, b"\xAA\xBB")
                    refreshed = await mm_b.find_matchmake_session_by_gathering_id([gid])
                    check("UpdateApplicationBuffer took effect",
                          len(refreshed) == 1 and refreshed[0].application_data == b"\xAA\xBB",
                          repr(refreshed[0].application_data) if refreshed else "no result")
                    check("participation count reflects both players",
                          len(refreshed) == 1 and refreshed[0].num_participants == 2,
                          str(refreshed[0].num_participants) if refreshed else "no result")

                    await mm_a.close_participation(gid)
                    closed = await mm_b.find_matchmake_session_by_gathering_id([gid])
                    check("CloseParticipation locked the session",
                          len(closed) == 1 and closed[0].open_participation is False,
                          str(closed[0].open_participation) if closed else "no result")

                    await mm_a.open_participation(gid)

                    print("\n=== 6. Host migration ===")

                    await mm_proto_a.update_session_host_v1(gid)
                    check("UpdateSessionHostV1 accepted", True)

                    print("\n=== 7. Ranking / leaderboards ===")

                    score_a = ranking.RankingScoreData()
                    score_a.category = 7
                    score_a.score = 9000        # * lap time in ms; lower is better
                    score_a.order = 0           # * ascending
                    score_a.update_mode = 0     # * keep the better score
                    score_a.groups = b"\x00\x00"
                    score_a.param = 111
                    await rank_a.upload_score(score_a, 0)

                    score_b = ranking.RankingScoreData()
                    score_b.category = 7
                    score_b.score = 8000        # * faster than A
                    score_b.order = 0
                    score_b.update_mode = 0
                    score_b.groups = b"\x00\x00"
                    score_b.param = 222
                    await rank_b.upload_score(score_b, 0)

                    order = ranking.RankingOrderParam()
                    order.order_calc = 0
                    order.group_index = 0xFF
                    order.group_num = 0
                    order.time_scope = 0
                    order.offset = 0
                    order.count = 10

                    board = await rank_a.get_ranking(0, 7, order, 0, 0)
                    check("GetRanking returned both scores",
                          board.total == 2, f"total={board.total}")
                    if len(board.data) >= 2:
                        first, second = board.data[0], board.data[1]
                        check("faster lap time ranks first (ascending order honoured)",
                              first.pid == PID_B and first.score == 8000,
                              f"rank1=pid {first.pid} score {first.score}")
                        check("ranks are 1-based and sequential",
                              first.rank == 1 and second.rank == 2,
                              f"ranks {first.rank},{second.rank}")
                        check("score param round-tripped", first.param == 222, str(first.param))

                    # * A worse score must not replace a better one under update_mode 0.
                    worse = ranking.RankingScoreData()
                    worse.category = 7
                    worse.score = 12000
                    worse.order = 0
                    worse.update_mode = 0
                    worse.groups = b"\x00\x00"
                    worse.param = 333
                    await rank_b.upload_score(worse, 0)

                    board = await rank_a.get_ranking(0, 7, order, 0, 0)
                    check("a slower time does not overwrite a personal best",
                          board.data[0].score == 8000, f"best={board.data[0].score}")

                    own = await rank_b.get_ranking(4, 7, order, 0, PID_B)
                    check("RankingMode 4 returns only the caller's entry",
                          own.total == 2 and len(own.data) == 1 and own.data[0].pid == PID_B,
                          f"{len(own.data)} entry, total {own.total}")

                    await rank_a.upload_common_data(b"ghost-data-blob", 42)
                    blob = await rank_a.get_common_data(42)
                    check("common data round-tripped", blob == b"ghost-data-blob", repr(blob))

                    approx = await rank_a.get_approx_order(7, order, 7500, 0, PID_A)
                    check("GetApproxOrder places a would-be world record first",
                          approx == 1, f"order={approx}")

                    print("\n=== 8. Leaving ===")

                    left = await mmext_b.end_participation(gid, "bye")
                    check("EndParticipation succeeded", left is True, str(left))

                    after = await mm_a.find_matchmake_session_by_gathering_id([gid])
                    check("participant count dropped after leaving",
                          len(after) == 1 and after[0].num_participants == 1,
                          str(after[0].num_participants) if after else "gathering gone")

            print("\n=== 9. Owner leaving destroys the gathering ===")

            await mm_a.update_application_buffer(gid, b"\x01")
            check("owner still controls the gathering", True)

    # * Client A's connection is now closed, which should reap its gathering.
    await anyio.sleep(1.0)

    async with backend.connect(s, HOST, AUTH_PORT) as be_c:
        async with be_c.login(str(PID_A), derive_password(PID_A), auth_info()) as client_c:
            mm_c = matchmaking.MatchmakeExtensionClient(client_c)
            remaining = await mm_c.find_matchmake_session_by_gathering_id([gid])
            check("gathering was reaped when the owner disconnected",
                  len(remaining) == 0, f"{len(remaining)} remaining")

            print("\n=== 10. Auto-matchmake ===")

            created = await mm_c.auto_matchmake_postpone(new_session(game_mode=5), "auto")
            check("AutoMatchmake created a session when none matched",
                  created.id > 0 and created.owner == PID_A, f"gid={created.id}")

            async with backend.connect(s, HOST, AUTH_PORT) as be_d:
                async with be_d.login(str(PID_B), derive_password(PID_B), auth_info()) as client_d:
                    mm_d = matchmaking.MatchmakeExtensionClient(client_d)
                    matched = await mm_d.auto_matchmake_postpone(new_session(game_mode=5), "auto")
                    check("AutoMatchmake matched the existing session instead of creating one",
                          matched.id == created.id, f"gid={matched.id} vs {created.id}")
                    check("auto-matched session reports two participants",
                          matched.num_participants == 2, str(matched.num_participants))

                    mismatched = await mm_d.auto_matchmake_postpone(new_session(game_mode=99), "auto")
                    check("AutoMatchmake creates a new session for a different game mode",
                          mismatched.id != created.id, f"gid={mismatched.id}")


def flatten(exc, depth=0):
    """anyio wraps everything in nested ExceptionGroups; show only the leaves."""
    if isinstance(exc, BaseExceptionGroup):
        for sub in exc.exceptions:
            flatten(sub, depth + 1)
        return

    print(f"\n!! {type(exc).__name__}: {exc}")
    tb = traceback.extract_tb(exc.__traceback__)
    for frame in tb:
        if "e2e_test.py" in frame.filename:
            print(f"   at e2e_test.py:{frame.lineno} in {frame.name}: {frame.line}")


if __name__ == "__main__":
    try:
        anyio.run(main)
    except BaseException as exc:  # noqa: BLE001
        flatten(exc)
        failures.append("unhandled exception")

    print("\n" + "=" * 60)
    print(f"passed: {len(passes)}   failed: {len(failures)}")
    if failures:
        print("failures:")
        for name in failures:
            print(f"  - {name}")
    print("=" * 60)

    sys.exit(1 if failures else 0)

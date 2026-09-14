# FAST Racing NEO — standalone NEX server

Protarium's standalone Wii U server for *FAST Racing NEO*, derived from
[Pretendo Network](https://github.com/PretendoNetwork)'s open-source NEX
stack — built on their `nex-go`, `nex-protocols-go`, and
`nex-protocols-common-go` libraries.

## Verified game identity

| Property | Value |
|---|---|
| Game server ID | `0x1012F000` |
| Access key | `811aa39f` |
| NEX libraries | `nex`, `nexmm`, `nexrk`, `nexut` 3.9.1 |
| Server build | `branch:origin/release/ngs/3.9.x.200x build:3_9_19_2005_0` |
| USA title | `000500001012F000` |
| Europe title | `00050000101D6000` |
| Japan title | `00050000101E4100` |

The values above come from Kinnay's RPX-derived Wii U and NEX databases and
were cross-checked against the European revision 65 `project.rpx` supplied for
this implementation.

## Implemented protocols

- Ticket Granting and Secure Connection
- NAT Traversal and P2P station URL exchange
- Match Making, Match Making Ext, and Matchmake Extension
- Ranking with persistent JSON-backed scores and common data
- Utility unique IDs and settings
- Participation, ownership, and host-migration notifications

## Local development

```sh
go test ./...
go run .
```

The integration harness in `tests/e2e_test.py` exercises two independent
clients end to end: Kerberos authentication, secure registration, session
creation/browse/join, P2P station URLs, session mutation, host migration,
rankings, disconnect cleanup, and auto-matchmaking. The Kinnay client checkout
used by the harness includes a local NEX 3.9 serialization correction so its
`MatchmakeSessionSearchCriteria` matches Pretendo's protocol schema.

The account service and this process must share the same 32-byte hexadecimal
password derivation secret. See `globals/config.go` for all `FAST_*` variables.
Production uses UDP `26500` for authentication and `26501` for the secure
server.

## Production layout

The service is intentionally separate from Wii Sports Club:

- binary: `/opt/fast-racing-neo/fast-racing-neo-server`
- state: `/opt/fast-racing-neo/data`
- unit: `fast-racing-neo.service`
- account routing: WSC's generic token issuer maps `1012f000` to UDP `26500`

## Deployed instance (Protarium)

Console-verified end to end on real Wii U hardware: authentication, secure
connection, matchmaking, ranking, and the full session lifecycle all confirmed
against the live Protarium deployment.

## License

AGPL-3.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE). No proprietary
Nintendo or Shin'en Multimedia code or assets are included.

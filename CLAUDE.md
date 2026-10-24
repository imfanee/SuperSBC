# SuperSBC: notes for an agent working in this repository

Read `docs/DECISIONS.md` first. Every assumption in this system is written
there as a numbered decision (D-01 to D-86). If you change behaviour, append a
new decision rather than editing an old one into something it never said.
`docs/ARCHITECTURE.md` has the component map, `docs/OPERATIONS.md` the
runbooks, `docs/TROUBLESHOOTING.md` the symptom index.

## Two stacks on one box

| | dev | live |
| --- | --- | --- |
| compose file | `docker-compose.yml` | `deploy/live/docker-compose.live.yml` |
| bring up | `make up` | `make live-up` |
| env | `.env` | `.env.live` (git-ignored) |
| containers | `supersbc-*` | `supersbc-live-*` |
| FreeSWITCH | private bridge network | host network, real SIP on 5060 |

Live carries **real customers, real money and real calls**. Prefer proving a
change in dev (`make up`, `make e2e`) before `make live-up`.

## Things that will bite you

* **The FreeSWITCH image is ours, not the stock one.** It is built from
  `deploy/freeswitch`, which replaces the pass-through `mod_g729` with a real
  `mod_bcg729` (D-86). Never point a compose file back at
  `safarov/freeswitch:1.10.12`, and never `docker exec` a module into a
  running container: the next deploy throws it away. The builder must stay
  `debian:12` (the runtime image is Debian 12 / glibc 2.36), and the vendored
  FreeSWITCH headers must be re-vendored if the base image version moves.
  `deploy/freeswitch/README.md` has the recipe. The G.729 tests in
  `tests/e2e/g729_test.go` are the proof it still works.
* **The SMTP app password must never be logged, printed, committed or put on a
  command line.** It lives only in `.env.live`, which is git-ignored, and
  `sbc-watchdog` reads it from the environment file.
* **Never put a customer IP in the firewall ban set.** `@banned` is matched
  before the accept rules in `deploy/live/nftables.conf`, so a ban beats a
  whitelist. `internal/httpapi/admin/authabuse.go` guards this; keep the
  guards.
* **Money is `decimal`, six decimal places, never float.**
* **Postgres has no ordering guarantee between data-modifying CTEs.** The
  hourly rollup once deleted and inserted in one statement and lost three
  weeks of dashboard data (D-79).
* **No em-dash anywhere in the tree** (D-41). `make lint` enforces it.
* Migrations are goose, in `internal/db/migrations`, applied by the API at
  start. Forward-only on live; `make rollback` is a dev tool.

## Codec and media rules, in one place

* The egress INVITE offers **only** the codecs the ingress INVITE offered that
  both the customer and the carrier allow. If there is no overlap it offers
  the carrier's list and the call is transcoded (D-85).
* `media_mode` is `anchor`, `smart`, `proxy` or `bypass`; the stricter of the
  customer's and the carrier's setting wins. `smart` proxies when the codecs
  match and anchors when they do not (D-84). All live customers and carriers
  are on `smart`.
* `matchedCodecs` in `internal/callcontrol/codec.go` decides both the egress
  codec list and the smart media mode. Keep it that way: if they ever
  disagree, a proxied call can get a codec neither leg can run.

## Commands worth knowing

```sh
make lint test-unit test-integration   # what CI would run
make up && make seed && make e2e       # dev stack plus the sipp suite
make live-up                           # deploy live (builds images, runs migrations)
make live-watchdog-check                # run every health check, send nothing
make live-backup                        # pg_dump of live
```

Live facts: hostname `sbc.softautonomi.com` with a Let's Encrypt certificate
renewed by `certbot.timer`, `supersbc-watchdog.timer` mails an all clear and
alerts on anything abnormal, `sbc-fwsync` keeps the nftables sets equal to the
database, HOMER captures SIP into `hep_proto_1_call` (not
`hep_proto_1_default`, which holds everything else).

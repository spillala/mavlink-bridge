# mavlink-bridge

Consumes PX4 SITL's MAVLink stream and reports flight state and fault
causes to the [dronefleet](https://github.com/spillala/dronefleet) API —
Phase A of `DRONEFLEET_NEXT_PHASE_PLAN.md`. Before this existed, nothing
consumed `px4-sitl-gazebo-svc:14550` and Agent 1's fault diagnoses had no
cause data to work with ("no specific cause available"). This closes that
gap: `STATUSTEXT` messages become `FaultEvent`s dronefleet stores and
Agent 1's existing `get_drone`/`get_fleet_status` tool calls surface —
no MCP or agent code changes needed (see
`docs/decisions/0001-separate-service-not-mcp-tool.md`).

**Read-only.** This process never writes to the flight controller — see
`docs/decisions/0002-mavlink-library-and-read-only-boundary.md` for exactly
what it does transmit (its own identity heartbeat) and why that isn't a
command path.

## Layout

```text
cmd/bridge/                 Live entry point: UDP → ingest → dronefleet API
cmd/replay/                 §11a replay harness: .tlog file → same ingest path
internal/mavlinksrc/        Decodes MAVLink frames from UDP (Live) or a .tlog file (ReplayFrames)
internal/ingest/            Aggregator: merges frames into Sample/Fault, the heartbeat-staleness watchdog
internal/dronefleetclient/  HTTP client matching dronefleet's TelemetryUpdate/FaultReport contract
internal/health/            /healthz, /readyz for k8s probes
docs/decisions/              ADRs
```

## Run

```sh
go build ./... && go test ./...

MAVLINK_ADDRESS=px4-sitl-gazebo-svc:14550 \
DRONEFLEET_URL=http://dronefleet-svc.dronefleet:80 \
DRONE_ID=drone-004 \
go run ./cmd/bridge
```

Flags mirror the env vars above (`-mavlink-address`, `-dronefleet-url`,
`-drone-id`, `-system-id`, `-heartbeat-stale`, `-health-addr`) — see
`cmd/bridge/main.go`.

## Replay a recorded log (§11a)

```sh
go run ./cmd/replay -file recorded.tlog -dronefleet-url http://localhost:8080 -drone-id drone-004
```

Feeds a `.tlog` file through the exact same `ingest.Aggregator` and
`dronefleetclient.Client` the live bridge uses, preserving the log's
original timestamps — including replaying a mid-log heartbeat gap through
the same fail-closed `CheckStale` path.

**Known gap:** public PX4 flight-review logs are ULog (`.ulg`), not MAVLink
`.tlog`. Converting a real corpus (e.g. via `pyulog`) into `.tlog` form is
not implemented here — see `internal/mavlinksrc.ReplayFrames`'s doc comment.
`internal/mavlinksrc/replay_test.go` proves the ingestion path against a
real checksummed MAVLink wire format either way (round-trip, and a
truncated/corrupt-log case), so the gap is specifically "get real recorded
bytes," not "does the ingestion path handle messy data."

## Known issue: `px4-sitl-gazebo-svc:14550` isn't actually serving MAVLink right now

Verified during Phase A implementation, not yet fixed (out of scope for
this repo — it's a `px4-sitl-gazebo`/`dockops-cicd` change): the live PX4
SITL pod's `/proc/net/udp` shows no socket bound to 14550 at all — the
actual bound UDP ports were 13030, 14280, 14580, 10317, 10318, and 18570.
`14580` is the closest match and a reasonable guess for the real GCS-facing
MAVLink port, but this wasn't confirmed against PX4's own startup log (the
pod had been running 6 days and its log buffer had already rotated past
the startup messages). Whoever picks this up next should either force a
fresh pod restart and capture the startup log, or just try `14580`.

Every other verification here — the unit tests, the Docker build, and a
full round-trip through a real checksummed `.tlog` — passed, including
against the real dronefleet API contract. The gap is specifically "which
port does PX4 actually answer on," not this bridge's own logic.

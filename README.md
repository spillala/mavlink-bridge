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

# Run as a sidecar sharing px4-sitl-gazebo's network namespace — see
# "Deployment" below for why MAVLINK_ADDRESS is a bind address, not a
# remote host:port.
MAVLINK_ADDRESS=:14550 \
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

## Deployment: this must run as a sidecar, not a standalone service

PX4 SITL sends its MAVLink stream *out* to its own loopback,
`127.0.0.1:14550` — it does not listen for a GCS to connect. A Kubernetes
Service can't deliver that (Services route inbound traffic to a pod, they
don't capture what a pod sends to its own loopback). `mavlink-bridge` binds
a UDP **server** on `:14550` and must run as a second container in the same
Pod as `px4-sitl-gazebo`, sharing its network namespace — not behind
`px4-sitl-gazebo-svc`. See
`docs/decisions/0003-udp-server-sidecar-not-client.md` for how this was
confirmed (packet capture against the live pod) and what it changes.

Verified end-to-end against the real running simulator: `drone-004`
received live `armed`/`flightMode`/`landedState`/position data, including
the AUTO.LOITER flight mode decode and PX4's default SITL home position
(Zürich) — see the ADR for the full trace.

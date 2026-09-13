# 3. mavlink-bridge is a UDP server and must run as a sidecar, not a standalone Service client

Date: 2026-09-13

## Status

Accepted — supersedes the connection direction assumed in ADR 0002 and in
`DRONEFLEET_NEXT_PHASE_PLAN.md` §5's architecture diagram (which shows
`mavlink-bridge` as an independent box reached via `px4-sitl-gazebo-svc`).

## Context

The initial implementation used `gomavlib.EndpointUDPClient{Address:
"px4-sitl-gazebo-svc:14550"}`, matching the plan's assumption that PX4
"broadcasts" and a separate pod could connect to it through the Service.
Against the real live cluster this failed immediately with a UDP
"connection refused" (ICMP port-unreachable).

Investigation (verified directly against the running `px4-sitl-gazebo` pod,
not assumed from docs):

- `/proc/net/udp` inside the PX4 container showed **nothing bound to
  14550** — consistent across a full pod restart. Bound ports were 13030,
  14280, 14580, 10317, 10318, and 18570.
- An ephemeral debug container attached to the pod (`kubectl debug
  --target=px4-sitl-gazebo`, sharing its network namespace) and listening
  on UDP 14550 with plain `nc` captured real MAVLink v2 frames (`0xFD`
  magic byte; decoded message IDs 30/`GLOBAL_POSITION_INT` and
  31/`ATTITUDE_QUATERNION`) arriving at `127.0.0.1:14550`.
- Community precedent
  ([gomavlib#233](https://github.com/bluenviron/gomavlib/discussions/233))
  confirms this is standard for PX4 SITL: the vehicle sends its MAVLink
  stream *out* to 14550 rather than listening there; a GCS-role consumer is
  expected to be the UDP server.

**Conclusion:** PX4 sends its GCS-facing MAVLink stream to its own
loopback, `127.0.0.1:14550`. A Kubernetes Service cannot deliver that —
Services route traffic *inbound* to a pod's containerPort; they have no
mechanism for capturing traffic a pod sends to its own loopback. The only
way to receive this stream is to share PX4's network namespace.

## Decision

`mavlinksrc.Connect` now binds `gomavlib.EndpointUDPServer` (server, not
client) — see `internal/mavlinksrc/live.go`. **`mavlink-bridge` must be
deployed as a sidecar container in the same Pod as `px4-sitl-gazebo`**,
listening on `:14550`, not as a standalone Deployment reached through
`px4-sitl-gazebo-svc`.

Verified end-to-end against the live cluster: attached as a temporary
`kubectl debug` ephemeral container sharing the real pod's network
namespace, `drone-004` in a throwaway dronefleet instance received real
telemetry — `armed: false`, `flightMode: "AUTO.LOITER"`,
`landedState: "ON_GROUND"`, position `47.3979709, 8.546164` (PX4's default
SITL home, Zürich). Cleaned up immediately after (process terminated,
scratch namespace deleted); nothing was left running against the live pod.

## Consequences

- `helm/px4-sitl-gazebo` (in `dockops-cicd`), not a new standalone
  `helm/mavlink-bridge` chart, needs the second container added to its
  existing Deployment — this repo has no Helm chart yet, deliberately, so
  this doesn't need undoing, just building correctly the first time.
- `DRONEFLEET_NEXT_PHASE_PLAN.md` §5's architecture diagram shows
  `mavlink-bridge` as an independent box; it should be redrawn as a sidecar
  inside the `px4-sitl-gazebo` pod. Flagged to the user, not edited here —
  the plan is their document.
- `MAVLINK_ADDRESS`/`-mavlink-address` changed meaning: it's now a bind
  address (default `:14550`), not a remote host:port.
- The three unrelated-but-unexplained bound ports found along the way
  (13030, 14280, 10317/10318) are presumably internal Gazebo↔PX4 simulation
  bridge links, not MAVLink — not investigated further, out of scope here.

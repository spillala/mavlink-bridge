# 2. MAVLink library, message set, and the read-only boundary

Date: 2026-09-13

## Status

Accepted

## Context

§4 of the plan is non-negotiable: all MAVLink interaction is read-only, and
any PR that writes to the flight controller is out of scope. §12 requires
rejecting any code path that writes to the flight controller. The
implementation needs a Go MAVLink library and a concrete boundary that
makes "read-only" true by construction, not by convention alone.

## Decision

**Library: `github.com/bluenviron/gomavlib/v3`** (the maintained fork of the
original `aler9/gomavlib`; MIT-licensed, actively released — v3.3.5 at
implementation time). It ships generated Go types for every dialect
including `common` (HEARTBEAT, EXTENDED_SYS_STATE, SYS_STATUS,
BATTERY_STATUS, STATUSTEXT, GLOBAL_POSITION_INT — everything §5's table
asks for), a UDP client endpoint matching PX4 SITL's GCS-connection
convention, and a `pkg/tlog` reader/writer for the §11a replay harness.

**Message set consumed** (`internal/ingest/aggregator.go`), matching §5's
table exactly:

| Message | Field(s) | Dronefleet field |
|---|---|---|
| HEARTBEAT | `base_mode & MAV_MODE_FLAG_SAFETY_ARMED` | `armed` |
| HEARTBEAT | `custom_mode` (PX4-decoded, see `px4mode.go`) | `flightMode` |
| EXTENDED_SYS_STATE | `landed_state` | `landedState` |
| SYS_STATUS | `voltage_battery`, `battery_remaining`, `onboard_control_sensors_health` | `voltageMv`, battery %, `sensorHealthBitmask` |
| BATTERY_STATUS | `battery_remaining` | battery % (fallback if SYS_STATUS omits it) |
| GLOBAL_POSITION_INT | `lat`, `lon`, `alt` | `lat`, `lng`, `altitude` |
| STATUSTEXT | `severity`, `text` | a `FaultEvent` (immediate, not batched) |

**Not implemented:** an `externalPower` signal. There is no standard
MAVLink field for "on external/bench power vs. battery" — §6.1's
deployment-gate checklist needs this, but determining it is a Phase B
concern for whoever builds `flight-state-gate`, likely via a different
signal entirely (companion-computer GPIO, a fixed battery ID convention,
etc.), not something MAVLink tells us directly. `ExternalPower` stays `nil`
in every `Sample` this bridge produces today.

**Read-only boundary:** `internal/mavlinksrc.Live` holds the only
`gomavlib.Node` in the codebase, and nothing calls `WriteMessageAll` or
`WriteMessageTo` anywhere in this repo. The one thing this process *does*
transmit is gomavlib's own automatic HEARTBEAT (`MAV_TYPE_GCS`, identity/
presence only, every 5s by default) — that's the standard MAVLink
"UDP client" handshake PX4 needs to recognize a GCS is listening and start
streaming to it, not a command to the vehicle. There is no code path here
that can arm, disarm, change mode, or otherwise command the flight
controller.

## Consequences

- `flightMode` decoding is PX4-specific (`internal/ingest/px4mode.go`,
  documented there). If this bridge is ever pointed at ArduPilot SITL, that
  decoding will be wrong — out of scope, since `px4-sitl-gazebo` is PX4-only
  per the existing simulator component.
- Public PX4 flight-review logs are ULog (`.ulg`), not MAVLink `.tlog` —
  `internal/mavlinksrc.ReplayFrames` reads `.tlog` only. Converting a real
  ULog corpus for §11a's replay harness is a follow-up, not done here; see
  that function's doc comment.

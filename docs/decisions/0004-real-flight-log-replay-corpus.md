# 4. §11a's real-flight-log corpus: source, conversion, and what's synthetic

Date: 2026-09-14

## Status

Accepted.

## Context

`DRONEFLEET_NEXT_PHASE_PLAN.md` §11a requires the replay harness (ADR 0001,
`cmd/replay`) to be proven against a **real** PX4 flight log before Phase A
is considered complete, not just SITL traffic — SITL never drops a packet,
never has sensor jitter, and never produces a real fault message. The
package doc on `mavlinksrc.ReplayFrames` already flagged the gap: public PX4
logs are ULog (`.ulg`), the aircraft's own onboard uORB topic recording, not
MAVLink `.tlog` — the wire protocol this bridge and `cmd/replay` actually
read. The two are different message sets; nothing in this repo converted
between them until now.

**Source log:** `testdata/real_flight_logs/sample_log_small.ulg`, from
[PX4/pyulog](https://github.com/PX4/pyulog)'s own test fixtures
(`test/sample_log_small.ulg`, fetched from the `main` branch,
sha256 `68d1020f688109f027dba08d2950247cc0ae34aaefdcf37e4a7d60838f3e1aa3`).
Decoding it (`internal/ingest/real_log_replay_test.go` does this on every
`go test` run) confirms it is a genuine flight, not a bench fixture invented
for pyulog's tests: real UTC timestamps (2021-04-21, from
`vehicle_gps_position.time_utc_usec`), a real GPS fix (63.42°N 10.41°E —
Trondheim, Norway, 14-15 satellites), and real logged text (`"[commander]
Takeoff detected"`, `"Landing detected"`, `"Disarmed by landing"`). It's a
short indoor/bench hop — armed 20.2s-25.8s in, airborne only ~1.1s of that —
not a long cross-country flight, but every value in it is a real airframe's,
not SITL's.

## Decision

`tools/ulog_to_tlog/convert.py` (Python — pyulog and pymavlink have no Go
equivalents worth vendoring for a one-off conversion tool) reads the ULog
and re-derives exactly the five MAVLink messages
`internal/ingest.Aggregator.HandleFrame` consumes, from the equivalent real
topics:

| MAVLink message | ULog source | Notes |
|---|---|---|
| `HEARTBEAT` | `actuator_armed.armed`, `commander_state.main_state` | Synthesized at 1Hz (PX4's real rate); `armed` bit and `custom_mode` are real, replayed continuously rather than only at ULog's own irregular publish times |
| `EXTENDED_SYS_STATE` | `vehicle_land_detected.landed` | Emitted only on each real transition |
| `SYS_STATUS` | `battery_status.voltage_v`, `.remaining` | Sampled at ~1Hz from the real (higher-rate) topic |
| `GLOBAL_POSITION_INT` | `vehicle_gps_position.{lat,lon,alt}` | Real GPS fixes, PX4's own log rate (~32 samples over 1174s) |
| `STATUSTEXT` | `ulog.logged_messages` | Real text and real severity — ULog stores MAV_SEVERITY as the ASCII digit of the level, decoded accordingly |

Every message's MAVLink timestamp is the ULog entry's own boot-relative
timestamp, converted to real UTC via the median offset between
`vehicle_gps_position.timestamp` and `.time_utc_usec` (not wall-clock
replay time) — this is what lets a heartbeat-gap test below land at the
flight's own real timeline.

Round-trip correctness was verified directly against `gomavlib`'s own
`tlog.Reader` (not assumed): each message type the converter writes decodes
back as the exact Go type `Aggregator.HandleFrame`'s switch matches —
confirmed several of these (`MessageHeartbeat`, `MessageGlobalPositionInt`)
are Go type aliases into `dialects/minimal`/`dialects/standard`, so a naive
`%T`-based check would have been misleading.

Checked in as fixtures (`internal/ingest/real_log_replay_test.go` reads
these on every `go test`, no Python/network needed at test time):

- `testdata/real_flight_logs/sample_log_small.tlog` — clean conversion,
  1219 frames.
- `testdata/real_flight_logs/sample_log_small_gap2s-17s.tlog` — same flight,
  with `--gap-start 2 --gap-duration 15` (relative to arming): every frame
  in that 15s window is dropped before writing.

### What the injected gap is, and why it's synthetic

The gap **is not in the real log** and cannot be: ULog is recorded onboard
to SD card, so it structurally cannot contain a lossy-radio-link dropout —
that failure mode exists only on the UDP link `mavlink-bridge` actually
listens to, which no public flight recording captures. `--gap-start`/
`--gap-duration` (`tools/ulog_to_tlog/convert.py`) drop every frame — not
just `HEARTBEAT` — in the requested window, modeling a total link blackout
rather than a single lost packet.

The window (2s-17s after arming) starts right at liftoff (real takeoff was
at +2.5s) so the dropout begins while genuinely airborne, and runs well past
the real landing/disarm (+3.6s/+5.6s) so the fixture proves the harder
case: heartbeats resume *after* the vehicle has actually landed, and the
bridge still must not report a stale `ON_GROUND` guess — only what a fresh
`EXTENDED_SYS_STATE` actually confirms. `TestRealLogReplay_
MidFlightHeartbeatGapFailsClosed` asserts both halves: the UNKNOWN
transition fires during the gap, and `landedState` never reverts to
`ON_GROUND` afterward without a real message re-confirming it — matching
§11a's requirement verbatim ("never a stale 'landed'").

## Consequences

- §11a's "Required before Phase B is considered complete" gate is now met:
  a real flight log, replayed through the exact live code path, proves both
  a real fault cause reaching dronefleet (`TestRealLogReplay_CleanFlight`)
  and the fail-closed heartbeat-gap behavior
  (`TestRealLogReplay_MidFlightHeartbeatGapFailsClosed`).
- This corpus does not exercise every row in §11a's table — no corrupt/
  truncated MAVLink bytes at the wire level (already covered separately by
  `mavlinksrc.ReplayFrames`'s truncated-log unit test), no out-of-order
  arrival, no clock skew. Those remain open; not claimed as done here.
- The source flight is a short bench hop, not a long outdoor mission — fine
  for proving the state-machine behavior this ADR targets, but not a
  substitute for a longer real flight if/when one is needed for other
  testing (e.g. sustained GPS dropout, which this fixture's log never has
  organically either).
- `tools/ulog_to_tlog/convert.py` requires `pyulog` and `pymavlink`
  (`pip install pyulog pymavlink`); it's a one-off conversion tool, not part
  of the Go build or CI — regenerate the fixtures only if a different/longer
  source log is adopted later.

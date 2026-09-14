#!/usr/bin/env python3
"""Convert a real PX4 ULog (.ulg) flight recording into a MAVLink .tlog that
mavlink-bridge's replay harness (cmd/replay) can read.

This is the §11a test-data conversion step DRONEFLEET_NEXT_PHASE_PLAN.md and
mavlink-bridge/cmd/replay/main.go both call out as not yet implemented: public
PX4 flight logs are ULog, not MAVLink telemetry logs, and the two are
different message sets entirely (ULog is PX4's internal uORB topic dump;
MAVLink is the wire protocol PX4 speaks to a GCS). This script bridges that
gap by re-deriving the handful of MAVLink messages ingest.Aggregator actually
consumes from the equivalent real ULog topics, using each topic's own
recorded timestamp.

What's REAL here: every armed/disarmed transition, landed/airborne
transition, GPS fix, battery voltage/remaining sample, and STATUSTEXT-
equivalent logged message comes from an actual flight's ULog. See
docs/decisions/0004-real-flight-log-replay-corpus.md for where the log came
from and exactly which fields map to which MAVLink message.

What's NOT real, if requested via --gap-start/--gap-duration: a MAVLink
heartbeat dropout. ULog is recorded onboard (SD card), so it structurally
cannot contain a lossy-radio-link dropout — that failure mode only exists on
the link this bridge actually listens to. Injecting it is how this script
still proves the §11a fail-closed requirement ("what does AirframeState
report when the heartbeat goes silent mid-flight?") against a real flight's
data, since no public log can supply that condition organically.

Usage:
    python3 convert.py sample_log_small.ulg out.tlog
    python3 convert.py sample_log_small.ulg out_gap.tlog --gap-start 40 --gap-duration 15
"""
import argparse
import statistics
import struct
import sys

from pyulog import ULog
from pymavlink.dialects.v20 import common as mavlink2

HEARTBEAT_HZ = 1.0
SYS_STATUS_HZ = 1.0
MAV_MODE_FLAG_SAFETY_ARMED = 128
MAV_AUTOPILOT_PX4 = 12  # MAV_AUTOPILOT_PX4
MAV_TYPE_QUADROTOR = 2


def px4_custom_mode(main_state):
    # PX4 commander_state.main_state -> MAVLink custom_mode (main<<16).
    # Only MANUAL (0) is exercised by this particular log; anything else
    # falls back to a value ingest.px4FlightMode renders as CUSTOM(n) rather
    # than a wrong guess.
    return {0: 1 << 16}.get(main_state, 0)


def utc_offset_us(ulog):
    """Real flights carry no epoch reference in most topics — only GPS does
    (time_utc_usec). Anchor every other topic's boot-relative timestamp to
    real UTC via the median GPS offset, so the replayed log carries the
    flight's actual date rather than the Unix epoch."""
    gps = ulog.get_dataset("vehicle_gps_position")
    offsets = [
        int(u) - int(t)
        for t, u in zip(gps.data["timestamp"], gps.data["time_utc_usec"])
        if u > 0
    ]
    if not offsets:
        sys.exit("no valid GPS UTC timestamp in this log; cannot anchor to real time")
    return statistics.median(offsets)


def build_events(ulog):
    """Returns a list of (boot_relative_us, encode_fn) pairs, unsorted."""
    events = []

    armed_ds = ulog.get_dataset("actuator_armed")
    armed_t = armed_ds.data["timestamp"]
    armed_v = armed_ds.data["armed"]

    commander_ds = ulog.get_dataset("commander_state")
    cmd_t = commander_ds.data["timestamp"]
    cmd_main = commander_ds.data["main_state"]

    def armed_at(t_us):
        idx = 0
        for i, at in enumerate(armed_t):
            if at <= t_us:
                idx = i
            else:
                break
        return bool(armed_v[idx])

    def main_state_at(t_us):
        idx = 0
        for i, ct in enumerate(cmd_t):
            if ct <= t_us:
                idx = i
            else:
                break
        return int(cmd_main[idx])

    t0, t1 = int(armed_t[0]), int(ulog.last_timestamp)
    step = int(1_000_000 / HEARTBEAT_HZ)
    t = t0
    while t <= t1:
        armed = armed_at(t)
        mode = px4_custom_mode(main_state_at(t))

        def encode(mav, armed=armed, mode=mode):
            base_mode = MAV_MODE_FLAG_SAFETY_ARMED if armed else 0
            return mav.heartbeat_encode(
                MAV_TYPE_QUADROTOR, MAV_AUTOPILOT_PX4, base_mode, mode, 4
            )

        events.append((t, encode))
        t += step

    land_ds = ulog.get_dataset("vehicle_land_detected")
    land_t = land_ds.data["timestamp"]
    land_v = land_ds.data["landed"]
    prev = None
    for i, lt in enumerate(land_t):
        landed = bool(land_v[i])
        if landed == prev:
            continue
        prev = landed
        state = 1 if landed else 2  # MAV_LANDED_STATE_ON_GROUND / IN_AIR

        def encode(mav, state=state):
            return mav.extended_sys_state_encode(0, state)

        events.append((int(lt), encode))

    bat_ds = ulog.get_dataset("battery_status")
    bat_t = bat_ds.data["timestamp"]
    bat_voltage = bat_ds.data["voltage_v"]
    bat_remaining = bat_ds.data["remaining"]
    step = int(1_000_000 / SYS_STATUS_HZ)
    next_sample = 0
    for i, bt in enumerate(bat_t):
        if bt < next_sample:
            continue
        next_sample = bt + step
        voltage_mv = int(round(bat_voltage[i] * 1000))
        remaining_pct = int(round(bat_remaining[i] * 100))

        def encode(mav, voltage_mv=voltage_mv, remaining_pct=remaining_pct):
            return mav.sys_status_encode(
                0, 0, 0,  # onboard_control_sensors_{present,enabled,health}: unused by aggregator
                0,  # load
                voltage_mv,
                -1,  # current_battery: unknown
                remaining_pct,
                0, 0, 0, 0, 0, 0,  # drop_rate_comm, errors_comm, errors_count1-4
            )

        events.append((int(bt), encode))

    gps_ds = ulog.get_dataset("vehicle_gps_position")
    gps_t = gps_ds.data["timestamp"]
    gps_lat = gps_ds.data["lat"]
    gps_lon = gps_ds.data["lon"]
    gps_alt = gps_ds.data["alt"]
    for i, gt in enumerate(gps_t):
        lat, lon, alt = int(gps_lat[i]), int(gps_lon[i]), int(gps_alt[i])

        def encode(mav, lat=lat, lon=lon, alt=alt):
            return mav.global_position_int_encode(
                0, lat, lon, alt, alt, 0, 0, 0, 0
            )

        events.append((int(gt), encode))

    for m in ulog.logged_messages:
        text = m.message.encode("utf-8", "replace")[:50]
        # ULog stores the severity as the ASCII digit character of the
        # syslog/MAV_SEVERITY level (e.g. log_level==54 is chr(54)=='6' ==
        # MAV_SEVERITY_INFO), not the level itself.
        severity = int(chr(m.log_level))

        def encode(mav, text=text, severity=severity):
            return mav.statustext_encode(severity, text)

        events.append((int(m.timestamp), encode))

    return events


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("ulog_path")
    ap.add_argument("tlog_path")
    ap.add_argument("--system-id", type=int, default=1)
    ap.add_argument(
        "--gap-start", type=float, default=None,
        help="Seconds into the flight (relative to arming) to start a SYNTHETIC dropped-link gap. "
             "Not present in the real log; models a lossy radio link, which an onboard ULog cannot.",
    )
    ap.add_argument("--gap-duration", type=float, default=None, help="Duration in seconds of the synthetic gap.")
    args = ap.parse_args()

    if (args.gap_start is None) != (args.gap_duration is None):
        ap.error("--gap-start and --gap-duration must be given together")

    ulog = ULog(args.ulog_path)
    offset = utc_offset_us(ulog)
    events = build_events(ulog)
    events.sort(key=lambda e: e[0])

    t_arm = int(ulog.get_dataset("actuator_armed").data["timestamp"][0])

    if args.gap_start is not None:
        gap_start_us = t_arm + int(args.gap_start * 1_000_000)
        gap_end_us = gap_start_us + int(args.gap_duration * 1_000_000)
        before = len(events)
        events = [e for e in events if not (gap_start_us <= e[0] < gap_end_us)]
        print(
            f"[convert] synthetic gap [{args.gap_start}s, {args.gap_start + args.gap_duration}s) "
            f"after arming dropped {before - len(events)} of {before} frames",
            file=sys.stderr,
        )

    with open(args.tlog_path, "wb") as f:
        mav = mavlink2.MAVLink(f, srcSystem=args.system_id, srcComponent=1)
        mav.seq = 0
        for boot_us, encode in events:
            epoch_us = boot_us + offset
            msg = encode(mav)
            f.write(struct.pack(">Q", int(epoch_us)))
            f.write(msg.pack(mav))

    print(f"[convert] wrote {len(events)} frames to {args.tlog_path}", file=sys.stderr)


if __name__ == "__main__":
    main()

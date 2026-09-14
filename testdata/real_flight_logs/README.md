# Real flight log fixtures (§11a)

See `docs/decisions/0004-real-flight-log-replay-corpus.md` for full
provenance and the field-by-field ULog→MAVLink mapping. Short version:

- `sample_log_small.ulg` — real PX4 flight recording, sourced from
  [PX4/pyulog](https://github.com/PX4/pyulog)'s test fixtures.
  sha256: `68d1020f688109f027dba08d2950247cc0ae34aaefdcf37e4a7d60838f3e1aa3`
- `sample_log_small.tlog` — the same flight, converted to MAVLink and
  replayable via `cmd/replay` or `mavlinksrc.ReplayFrames` directly.
- `sample_log_small_gap2s-17s.tlog` — same flight, with a **synthetic**
  15-second total-link dropout injected 2s after arming (real takeoff was
  at +2.5s). Proves the §11a fail-closed requirement; the gap itself is not
  present in the real log (see the ADR for why it can't be).

Regenerate after changing `tools/ulog_to_tlog/convert.py`:

```
pip install pyulog pymavlink
python3 tools/ulog_to_tlog/convert.py \
    testdata/real_flight_logs/sample_log_small.ulg \
    testdata/real_flight_logs/sample_log_small.tlog

python3 tools/ulog_to_tlog/convert.py \
    testdata/real_flight_logs/sample_log_small.ulg \
    testdata/real_flight_logs/sample_log_small_gap2s-17s.tlog \
    --gap-start 2 --gap-duration 15
```

`internal/ingest/real_log_replay_test.go` reads the `.tlog` fixtures
directly — regenerating them is only needed if the source log or the
converter's mapping changes, not for normal `go test` runs.

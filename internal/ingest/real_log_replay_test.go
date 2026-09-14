package ingest_test

import (
	"os"
	"testing"
	"time"

	"github.com/spillala/mavlink-bridge/internal/ingest"
	"github.com/spillala/mavlink-bridge/internal/mavlinksrc"
)

// This is the §11a real-flight-log proof: testdata/real_flight_logs holds a
// real PX4 flight recording (see testdata/real_flight_logs/README.md and
// docs/decisions/0004-real-flight-log-replay-corpus.md for provenance and
// exactly how the .ulg was converted to these .tlog fixtures), replayed
// through the *exact* Aggregator code path live SITL and the live sidecar
// use. SITL alone can't prove this: it never drops a heartbeat, never has
// real sensor jitter, and never contains real logged fault text.

func replayFixture(t *testing.T, path string) ([]ingest.Sample, []ingest.Fault) {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	frames, err := mavlinksrc.ReplayFrames(f)
	if err != nil {
		t.Fatalf("ReplayFrames(%s): %v", path, err)
	}

	var samples []ingest.Sample
	var faults []ingest.Fault
	agg := ingest.NewAggregator("real-log-drone", 1,
		func(s ingest.Sample) { samples = append(samples, s) },
		func(fl ingest.Fault) { faults = append(faults, fl) },
	)

	const heartbeatStale = 10 * time.Second
	for _, frm := range frames {
		if s, stale := agg.CheckStale(frm.Time, heartbeatStale); stale {
			samples = append(samples, s)
		}
		agg.HandleFrame(frm)
	}

	return samples, faults
}

func TestRealLogReplay_CleanFlight(t *testing.T) {
	samples, faults := replayFixture(t, "../../testdata/real_flight_logs/sample_log_small.tlog")

	if len(samples) == 0 {
		t.Fatal("expected at least one sample from a real flight recording")
	}

	wantFaultText := map[string]bool{
		"[commander] Takeoff detected":    false,
		"[commander] Landing detected":    false,
		"[commander] Disarmed by landing": false,
	}
	for _, fl := range faults {
		if _, ok := wantFaultText[fl.Message]; ok {
			wantFaultText[fl.Message] = true
		}
		if fl.Severity != ingest.SeverityInfo {
			t.Errorf("fault %q: expected severity info (real ULog log_level), got %s", fl.Message, fl.Severity)
		}
	}
	for text, seen := range wantFaultText {
		if !seen {
			t.Errorf("expected a real STATUSTEXT-equivalent fault with message %q, never saw it", text)
		}
	}

	var sawArmed, sawInAir bool
	for _, s := range samples {
		if s.Armed != nil && *s.Armed {
			sawArmed = true
		}
		if s.LandedState == ingest.LandedStateInAir {
			sawInAir = true
		}
	}
	if !sawArmed {
		t.Error("expected at least one sample with Armed=true from the real log's arm window")
	}
	if !sawInAir {
		t.Error("expected at least one sample with LandedState=IN_AIR from the real log's flight window")
	}

	// A clean replay (no injected dropout) must never fail closed — the
	// heartbeat never actually goes silent in this fixture.
	for _, s := range samples {
		if s.LandedState == ingest.LandedStateUnknown && s.Armed == nil {
			t.Errorf("unexpected fail-closed UNKNOWN sample at %s in the gap-free fixture", s.RecordedAt)
		}
	}
}

// TestRealLogReplay_MidFlightHeartbeatGapFailsClosed is §11a's required
// proof, run against real (not SITL) sensor data: "what does AirframeState
// report when the heartbeat goes silent mid-flight? The answer must be
// UNKNOWN -> fail closed, never a stale 'landed'."
//
// The fixture is the same real flight with a synthetic ~15s total-link
// dropout injected 2s after arming — i.e. starting right at liftoff. See
// docs/decisions/0004-real-flight-log-replay-corpus.md for why this
// particular gap can't come from the real log itself: it's an onboard SD
// recording, which structurally cannot contain a lossy-radio-link outage.
func TestRealLogReplay_MidFlightHeartbeatGapFailsClosed(t *testing.T) {
	samples, _ := replayFixture(t, "../../testdata/real_flight_logs/sample_log_small_gap2s-17s.tlog")

	var gapSample *ingest.Sample
	for i := range samples {
		s := &samples[i]
		if s.LandedState == ingest.LandedStateUnknown && s.Armed == nil {
			gapSample = s
			break
		}
	}
	if gapSample == nil {
		t.Fatal("expected a fail-closed UNKNOWN sample during the injected heartbeat gap, found none")
	}

	// Once the fixture resumes past the gap, later samples must not revert
	// to a stale ON_GROUND/armed guess before a fresh EXTENDED_SYS_STATE or
	// HEARTBEAT has actually re-confirmed it — landedState in particular
	// must stay UNKNOWN until a real EXTENDED_SYS_STATE arrives, since none
	// does in the remainder of this fixture.
	for _, s := range samples {
		if !s.RecordedAt.After(gapSample.RecordedAt) {
			continue
		}
		if s.LandedState == ingest.LandedStateOnGround {
			t.Errorf("sample at %s reverted to a stale ON_GROUND guess after the heartbeat gap "+
				"without a fresh EXTENDED_SYS_STATE; must stay UNKNOWN", s.RecordedAt)
		}
	}
}

package ingest

import (
	"testing"
	"time"

	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"

	"github.com/spillala/mavlink-bridge/internal/mavlinksrc"
)

func heartbeat(armed bool, customMode uint32, t time.Time, systemID byte) mavlinksrc.Frame {
	baseMode := common.MAV_MODE_FLAG(0)
	if armed {
		baseMode |= common.MAV_MODE_FLAG_SAFETY_ARMED
	}
	return mavlinksrc.Frame{
		SystemID: systemID,
		Time:     t,
		Message:  &common.MessageHeartbeat{BaseMode: baseMode, CustomMode: customMode},
	}
}

func extendedSysState(state common.MAV_LANDED_STATE, t time.Time, systemID byte) mavlinksrc.Frame {
	return mavlinksrc.Frame{SystemID: systemID, Time: t, Message: &common.MessageExtendedSysState{LandedState: state}}
}

func globalPos(latE7, lonE7, altMm int32, t time.Time, systemID byte) mavlinksrc.Frame {
	return mavlinksrc.Frame{SystemID: systemID, Time: t, Message: &common.MessageGlobalPositionInt{Lat: latE7, Lon: lonE7, Alt: altMm}}
}

func sysStatus(batteryPct int8, voltageMv uint16, t time.Time, systemID byte) mavlinksrc.Frame {
	return mavlinksrc.Frame{SystemID: systemID, Time: t, Message: &common.MessageSysStatus{BatteryRemaining: batteryPct, VoltageBattery: voltageMv}}
}

func statustext(sev common.MAV_SEVERITY, text string, t time.Time, systemID byte) mavlinksrc.Frame {
	return mavlinksrc.Frame{SystemID: systemID, Time: t, Message: &common.MessageStatustext{Severity: sev, Text: text}}
}

// A complete sample requires GLOBAL_POSITION_INT and SYS_STATUS/BATTERY_STATUS
// to have arrived at least once before a HEARTBEAT will emit anything —
// dronefleet's TelemetryUpdate requires lat/lng/altitude/battery.
func primeCompleteSample(agg *Aggregator, at time.Time, systemID byte) {
	agg.HandleFrame(globalPos(129716000, 775946000, 40000, at, systemID))
	agg.HandleFrame(sysStatus(88, 12000, at, systemID))
}

func TestHeartbeatArmedBitExtraction(t *testing.T) {
	var got []Sample
	agg := NewAggregator("drone-001", 1, func(s Sample) { got = append(got, s) }, nil)

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	primeCompleteSample(agg, now, 1)
	agg.HandleFrame(heartbeat(true, 0, now, 1))

	if len(got) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(got))
	}
	if got[0].Armed == nil || !*got[0].Armed {
		t.Fatalf("expected armed=true, got %+v", got[0].Armed)
	}

	agg.HandleFrame(heartbeat(false, 0, now.Add(time.Second), 1))
	if got[1].Armed == nil || *got[1].Armed {
		t.Fatalf("expected armed=false, got %+v", got[1].Armed)
	}
}

func TestLandedStateMapping(t *testing.T) {
	cases := []struct {
		in   common.MAV_LANDED_STATE
		want LandedState
	}{
		{common.MAV_LANDED_STATE_ON_GROUND, LandedStateOnGround},
		{common.MAV_LANDED_STATE_IN_AIR, LandedStateInAir},
		{common.MAV_LANDED_STATE_TAKEOFF, LandedStateTakeoff},
		{common.MAV_LANDED_STATE_LANDING, LandedStateLanding},
		{common.MAV_LANDED_STATE_UNDEFINED, LandedStateUnknown},
	}
	for _, tc := range cases {
		var got []Sample
		agg := NewAggregator("drone-001", 1, func(s Sample) { got = append(got, s) }, nil)
		now := time.Now()
		primeCompleteSample(agg, now, 1)
		agg.HandleFrame(extendedSysState(tc.in, now, 1))
		agg.HandleFrame(heartbeat(false, 0, now, 1))

		if len(got) != 1 || got[0].LandedState != tc.want {
			t.Fatalf("landed state %v: expected %v, got %+v", tc.in, tc.want, got)
		}
	}
}

func TestStatustextBecomesFault(t *testing.T) {
	var faults []Fault
	agg := NewAggregator("drone-004", 1, nil, func(f Fault) { faults = append(faults, f) })

	now := time.Now()
	agg.HandleFrame(statustext(common.MAV_SEVERITY_ERROR, "ESC 3 overcurrent", now, 1))

	if len(faults) != 1 {
		t.Fatalf("expected 1 fault, got %d", len(faults))
	}
	if faults[0].Severity != SeverityError || faults[0].Message != "ESC 3 overcurrent" || faults[0].DroneID != "drone-004" {
		t.Fatalf("unexpected fault: %+v", faults[0])
	}
}

func TestFramesFromOtherSystemIDAreIgnored(t *testing.T) {
	var got []Sample
	var faults []Fault
	agg := NewAggregator("drone-001", 1, func(s Sample) { got = append(got, s) }, func(f Fault) { faults = append(faults, f) })

	now := time.Now()
	primeCompleteSample(agg, now, 99) // wrong system ID
	agg.HandleFrame(heartbeat(true, 0, now, 99))
	agg.HandleFrame(statustext(common.MAV_SEVERITY_CRITICAL, "should be ignored", now, 99))

	if len(got) != 0 || len(faults) != 0 {
		t.Fatalf("expected frames from system 99 to be ignored, got samples=%d faults=%d", len(got), len(faults))
	}
}

func TestHeartbeatWithoutPositionOrBatteryDoesNotEmit(t *testing.T) {
	var got []Sample
	agg := NewAggregator("drone-001", 1, func(s Sample) { got = append(got, s) }, nil)

	// No GLOBAL_POSITION_INT / SYS_STATUS seen yet — dronefleet requires
	// lat/lng/altitude/battery, so nothing should be posted.
	agg.HandleFrame(heartbeat(true, 0, time.Now(), 1))

	if len(got) != 0 {
		t.Fatalf("expected no sample without a complete position/battery reading, got %+v", got)
	}
}

func TestCheckStaleFailsClosedToUnknown(t *testing.T) {
	var got []Sample
	agg := NewAggregator("drone-001", 1, func(s Sample) { got = append(got, s) }, nil)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	primeCompleteSample(agg, base, 1)
	agg.HandleFrame(extendedSysState(common.MAV_LANDED_STATE_IN_AIR, base, 1))
	agg.HandleFrame(heartbeat(true, 4<<16|4, base, 1)) // AUTO.MISSION, armed

	if len(got) != 1 || got[0].LandedState != LandedStateInAir || got[0].Armed == nil || !*got[0].Armed {
		t.Fatalf("expected a known in-air/armed sample before the gap, got %+v", got)
	}

	// No heartbeat for 15s, threshold is 10s: must fail closed.
	sample, stale := agg.CheckStale(base.Add(15*time.Second), 10*time.Second)
	if !stale {
		t.Fatal("expected CheckStale to report stale after the heartbeat gap")
	}
	if sample.LandedState != LandedStateUnknown {
		t.Fatalf("expected LandedState=UNKNOWN on staleness, got %v", sample.LandedState)
	}
	if sample.Armed != nil {
		t.Fatalf("expected Armed=nil (unknown) on staleness, got %v", *sample.Armed)
	}
	// Position/battery are preserved, not nulled — TelemetryUpdate requires
	// them regardless, and GPS/battery don't need to fail closed the way
	// arm/landed state does.
	if sample.Lat == nil || sample.BatteryPct == nil {
		t.Fatalf("expected position/battery to be preserved on staleness, got %+v", sample)
	}

	// A second check without a new heartbeat must not re-emit.
	_, staleAgain := agg.CheckStale(base.Add(16*time.Second), 10*time.Second)
	if staleAgain {
		t.Fatal("expected CheckStale not to re-report an already-known stale state")
	}
}

func TestCheckStaleBeforeAnyCompleteSampleReportsNothing(t *testing.T) {
	agg := NewAggregator("drone-001", 1, nil, nil)
	// Never received a single complete sample — nothing valid to report yet;
	// dronefleet already shows "no telemetry" for this drone.
	_, stale := agg.CheckStale(time.Now().Add(time.Hour), 10*time.Second)
	if stale {
		t.Fatal("expected no stale report before any complete sample was ever seen")
	}
}

func TestCheckStaleClearsAfterHeartbeatResumes(t *testing.T) {
	var got []Sample
	agg := NewAggregator("drone-001", 1, func(s Sample) { got = append(got, s) }, nil)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	primeCompleteSample(agg, base, 1)
	agg.HandleFrame(heartbeat(true, 0, base, 1))

	agg.CheckStale(base.Add(15*time.Second), 10*time.Second)

	// Heartbeat resumes.
	agg.HandleFrame(heartbeat(false, 0, base.Add(16*time.Second), 1))

	last := got[len(got)-1]
	if last.LandedState == LandedStateUnknown && last.Armed == nil {
		t.Fatal("expected a resumed heartbeat to clear the stale/unknown state")
	}
	if last.Armed == nil || *last.Armed {
		t.Fatalf("expected armed=false on the resumed heartbeat, got %+v", last.Armed)
	}
}

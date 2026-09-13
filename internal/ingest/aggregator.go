package ingest

import (
	"sync"
	"time"

	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"

	"github.com/spillala/mavlink-bridge/internal/mavlinksrc"
)

// Aggregator merges MAVLink messages from one flight controller (identified
// by MAVLink SystemID) into dronefleet's Sample/Fault domain shape. It
// tracks exactly one vehicle — mapping many MAVLink systems to many
// dronefleet drone IDs is Phase E's fleet-scale concern, not this one's.
//
// Not safe to share HandleFrame/CheckStale calls without the caller
// serializing them itself in spirit — internally it's mutex-guarded, so
// concurrent calls are safe, but there is exactly one merged state, matching
// the one vehicle it tracks.
type Aggregator struct {
	droneID  string
	systemID byte

	onSample func(Sample)
	onFault  func(Fault)

	mu            sync.Mutex
	state         Sample
	lastHeartbeat time.Time
}

// NewAggregator tracks MAVLink SystemID systemID and reports it to
// dronefleet as droneID. onSample/onFault are called synchronously from
// whichever goroutine calls HandleFrame/CheckStale — keep them fast, or
// hand off to your own queue.
func NewAggregator(droneID string, systemID byte, onSample func(Sample), onFault func(Fault)) *Aggregator {
	return &Aggregator{
		droneID:  droneID,
		systemID: systemID,
		onSample: onSample,
		onFault:  onFault,
		state:    Unknown(droneID, time.Time{}),
	}
}

// HandleFrame folds one decoded MAVLink frame into the tracked vehicle's
// state. Frames from any other SystemID are ignored. A HEARTBEAT — the
// message every MAVLink system sends at ~1Hz — is what triggers emitting
// the merged Sample snapshot; STATUSTEXT emits a Fault immediately instead
// of waiting for the next heartbeat, since a fault cause is exactly what
// Agent 1 needs as soon as it exists.
func (a *Aggregator) HandleFrame(f mavlinksrc.Frame) {
	if f.SystemID != a.systemID {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	switch msg := f.Message.(type) {
	case *common.MessageHeartbeat:
		a.lastHeartbeat = f.Time
		armed := msg.BaseMode&common.MAV_MODE_FLAG_SAFETY_ARMED != 0
		mode := px4FlightMode(msg.CustomMode)

		a.state.DroneID = a.droneID
		a.state.Source = "mavlink"
		a.state.RecordedAt = f.Time
		a.state.Armed = &armed
		a.state.FlightMode = &mode

		// dronefleet's TelemetryUpdate requires lat/lng/altitude/battery.
		// GLOBAL_POSITION_INT and BATTERY_STATUS/SYS_STATUS arrive as
		// separate messages, so skip emitting until we've seen at least one
		// of each — state keeps accumulating and the next heartbeat (well
		// under a second later, in practice) almost always has both.
		if a.onSample != nil && a.state.Lat != nil && a.state.BatteryPct != nil {
			a.onSample(a.state)
		}

	case *common.MessageExtendedSysState:
		a.state.LandedState = landedStateFrom(msg.LandedState)

	case *common.MessageSysStatus:
		health := int64(msg.OnboardControlSensorsHealth)
		a.state.SensorHealthBitmask = &health
		if msg.VoltageBattery != 0xFFFF { // UINT16_MAX means "unknown" per the MAVLink spec
			mv := int(msg.VoltageBattery)
			a.state.VoltageMv = &mv
		}
		if msg.BatteryRemaining >= 0 { // -1 means "unknown" per the MAVLink spec
			pct := int(msg.BatteryRemaining)
			a.state.BatteryPct = &pct
		}

	case *common.MessageBatteryStatus:
		if msg.BatteryRemaining >= 0 {
			pct := int(msg.BatteryRemaining)
			a.state.BatteryPct = &pct
		}

	case *common.MessageGlobalPositionInt:
		lat := float64(msg.Lat) / 1e7
		lng := float64(msg.Lon) / 1e7
		alt := float64(msg.Alt) / 1000.0
		a.state.Lat, a.state.Lng, a.state.Alt = &lat, &lng, &alt

	case *common.MessageStatustext:
		if a.onFault != nil {
			a.onFault(Fault{
				DroneID:    a.droneID,
				Severity:   severityFrom(msg.Severity),
				Message:    msg.Text,
				RecordedAt: f.Time,
			})
		}
	}
}

// CheckStale reports whether the tracked vehicle's heartbeat has gone
// silent for at least staleAfter as of now. On the transition into
// staleness it clears the safety-relevant fields (armed, landedState,
// flightMode, ...) to unknown and returns the result for the caller to
// report. Position and battery are deliberately preserved rather than
// nulled — GPS/battery telemetry doesn't need to fail closed the way
// arm/landed state does, and dronefleet's TelemetryUpdate requires them
// regardless.
//
// It returns (Sample{}, false) in three cases: the heartbeat is still
// fresh; staleness was already reported (don't spam duplicate UNKNOWN
// writes every tick); or no complete sample has ever been seen, so there
// is nothing valid to report yet — dronefleet already shows "no telemetry"
// for this drone, which is safely ambiguous on its own.
//
// This is the fail-closed rule from §4/§11a: absence of a heartbeat is
// itself a state, and it is never read as "probably still landed".
func (a *Aggregator) CheckStale(now time.Time, staleAfter time.Duration) (Sample, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.lastHeartbeat.IsZero() && now.Sub(a.lastHeartbeat) < staleAfter {
		return Sample{}, false
	}
	if a.state.Lat == nil || a.state.BatteryPct == nil {
		return Sample{}, false
	}
	if a.state.LandedState == LandedStateUnknown && a.state.Armed == nil {
		return Sample{}, false
	}

	a.state.LandedState = LandedStateUnknown
	a.state.Armed = nil
	a.state.FlightMode = nil
	a.state.VoltageMv = nil
	a.state.SensorHealthBitmask = nil
	a.state.RecordedAt = now
	return a.state, true
}

func landedStateFrom(s common.MAV_LANDED_STATE) LandedState {
	switch s {
	case common.MAV_LANDED_STATE_ON_GROUND:
		return LandedStateOnGround
	case common.MAV_LANDED_STATE_IN_AIR:
		return LandedStateInAir
	case common.MAV_LANDED_STATE_TAKEOFF:
		return LandedStateTakeoff
	case common.MAV_LANDED_STATE_LANDING:
		return LandedStateLanding
	default: // includes MAV_LANDED_STATE_UNDEFINED
		return LandedStateUnknown
	}
}

func severityFrom(s common.MAV_SEVERITY) Severity {
	switch s {
	case common.MAV_SEVERITY_EMERGENCY:
		return SeverityEmergency
	case common.MAV_SEVERITY_ALERT:
		return SeverityAlert
	case common.MAV_SEVERITY_CRITICAL:
		return SeverityCritical
	case common.MAV_SEVERITY_ERROR:
		return SeverityError
	case common.MAV_SEVERITY_WARNING:
		return SeverityWarning
	case common.MAV_SEVERITY_NOTICE:
		return SeverityNotice
	case common.MAV_SEVERITY_INFO:
		return SeverityInfo
	default:
		return SeverityDebug
	}
}

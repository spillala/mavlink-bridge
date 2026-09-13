// Package ingest turns decoded MAVLink messages into the domain events
// mavlink-bridge writes to the dronefleet API. It knows nothing about UDP,
// files, or HTTP — Aggregator takes a stream of Frames (from either the live
// listener or the replay harness) and emits Samples and Faults. Keeping this
// logic transport-agnostic is what lets §11a's replay harness exercise the
// exact same code path live SITL traffic does.
package ingest

import "time"

// Severity mirrors dronefleet's FaultSeverity enum (MAV_SEVERITY, lowercased).
type Severity string

const (
	SeverityEmergency Severity = "emergency"
	SeverityAlert     Severity = "alert"
	SeverityCritical  Severity = "critical"
	SeverityError     Severity = "error"
	SeverityWarning   Severity = "warning"
	SeverityNotice    Severity = "notice"
	SeverityInfo      Severity = "info"
	SeverityDebug     Severity = "debug"
)

// LandedState mirrors dronefleet's LandedState enum. Unknown is the
// fail-closed default — never inferred as OnGround.
type LandedState string

const (
	LandedStateOnGround LandedState = "ON_GROUND"
	LandedStateInAir    LandedState = "IN_AIR"
	LandedStateTakeoff  LandedState = "TAKEOFF"
	LandedStateLanding  LandedState = "LANDING"
	LandedStateUnknown  LandedState = "UNKNOWN"
)

// Fault is one STATUSTEXT-derived event, reported to dronefleet immediately
// as it arrives — this is what fixes Agent 1's "no specific cause available".
type Fault struct {
	DroneID    string
	Severity   Severity
	Message    string
	RecordedAt time.Time
}

// Sample is a merged flight-state snapshot, reported to dronefleet on the
// same cadence as HEARTBEAT (or synthetically, by the staleness watchdog,
// when HEARTBEAT stops arriving at all).
type Sample struct {
	DroneID             string
	RecordedAt          time.Time
	Source              string // "mavlink" for live/replayed traffic
	Armed               *bool
	LandedState         LandedState // never the zero value: Unknown until proven otherwise
	ExternalPower       *bool
	VoltageMv           *int
	FlightMode          *string
	Lat, Lng, Alt       *float64
	BatteryPct          *int
	SensorHealthBitmask *int64
}

// Unknown returns the fail-closed starting state for droneID before any
// MAVLink data has been seen: every field absent except LandedState, which
// is explicitly UNKNOWN. Not directly postable to dronefleet on its own —
// TelemetryUpdate requires lat/lng/altitude/battery — it's Aggregator's
// internal starting point; see Aggregator.CheckStale for how staleness
// after a real sample is reported instead.
func Unknown(droneID string, at time.Time) Sample {
	return Sample{
		DroneID:     droneID,
		RecordedAt:  at,
		Source:      "mavlink",
		LandedState: LandedStateUnknown,
	}
}

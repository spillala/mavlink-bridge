package ingest

import "fmt"

// px4FlightMode decodes HEARTBEAT.custom_mode into a human-readable PX4
// flight mode string. This is PX4-specific: custom_mode packs a main mode
// into bits 16-23 and (for AUTO) a sub-mode into bits 24-31 — see PX4's
// own px4_custom_mode.h. ArduPilot packs custom_mode differently; this
// bridge targets PX4 SITL only, per the plan's px4-sitl-gazebo component.
//
// Unrecognized values fall back to a numeric form rather than guessing —
// better to show "CUSTOM(1234)" than a wrong label.
func px4FlightMode(customMode uint32) string {
	main := (customMode >> 16) & 0xFF
	sub := (customMode >> 24) & 0xFF

	switch main {
	case 1:
		return "MANUAL"
	case 2:
		return "ALTCTL"
	case 3:
		return "POSCTL"
	case 4:
		return px4AutoSubMode(sub)
	case 5:
		return "ACRO"
	case 6:
		return "OFFBOARD"
	case 7:
		return "STABILIZED"
	case 8:
		return "RATTITUDE"
	default:
		return fmt.Sprintf("CUSTOM(%d)", customMode)
	}
}

func px4AutoSubMode(sub uint32) string {
	switch sub {
	case 1:
		return "AUTO.READY"
	case 2:
		return "AUTO.TAKEOFF"
	case 3:
		return "AUTO.LOITER"
	case 4:
		return "AUTO.MISSION"
	case 5:
		return "AUTO.RTL"
	case 6:
		return "AUTO.LAND"
	case 8:
		return "AUTO.FOLLOW_TARGET"
	case 9:
		return "AUTO.PRECLAND"
	default:
		return fmt.Sprintf("AUTO(%d)", sub)
	}
}

package ingest

import "testing"

func TestPX4FlightModeDecoding(t *testing.T) {
	cases := []struct {
		name       string
		customMode uint32
		want       string
	}{
		{"manual", 1 << 16, "MANUAL"},
		{"posctl", 3 << 16, "POSCTL"},
		{"auto mission", 4<<16 | 4<<24, "AUTO.MISSION"},
		{"auto rtl", 4<<16 | 5<<24, "AUTO.RTL"},
		{"auto land", 4<<16 | 6<<24, "AUTO.LAND"},
		{"offboard", 6 << 16, "OFFBOARD"},
		{"unrecognized main mode", 99 << 16, "CUSTOM(6488064)"},
		{"unrecognized auto sub mode", 4<<16 | 250<<24, "AUTO(250)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := px4FlightMode(tc.customMode); got != tc.want {
				t.Errorf("px4FlightMode(%d) = %q, want %q", tc.customMode, got, tc.want)
			}
		})
	}
}

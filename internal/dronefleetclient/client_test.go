package dronefleetclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/spillala/mavlink-bridge/internal/ingest"
)

func floatPtr(f float64) *float64 { return &f }
func intPtr(i int) *int           { return &i }
func boolPtr(b bool) *bool        { return &b }

func TestPostSampleRequestShape(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
	}))
	defer srv.Close()

	c := New(srv.URL)
	mode := "AUTO.MISSION"
	sample := ingest.Sample{
		DroneID:     "drone-004",
		RecordedAt:  time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		Source:      "mavlink",
		Armed:       boolPtr(true),
		LandedState: ingest.LandedStateInAir,
		FlightMode:  &mode,
		Lat:         floatPtr(12.9),
		Lng:         floatPtr(77.5),
		Alt:         floatPtr(40),
		BatteryPct:  intPtr(88),
	}

	if err := c.PostSample(context.TODO(), sample); err != nil {
		t.Fatalf("PostSample: %v", err)
	}

	if gotPath != "/api/v1/fleet/drone-004/telemetry" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	// Field names must match dronefleet's openapi.yaml TelemetryUpdate schema exactly.
	for _, field := range []string{"lat", "lng", "altitude", "battery", "armed", "landedState", "flightMode", "source", "recordedAt"} {
		if _, ok := gotBody[field]; !ok {
			t.Errorf("expected field %q in request body, got %+v", field, gotBody)
		}
	}
	if gotBody["landedState"] != "IN_AIR" {
		t.Errorf("expected landedState=IN_AIR, got %v", gotBody["landedState"])
	}
}

func TestPostSampleRejectsIncompleteSample(t *testing.T) {
	c := New("http://unused")
	err := c.PostSample(context.TODO(), ingest.Sample{DroneID: "drone-001", LandedState: ingest.LandedStateUnknown})
	if err == nil {
		t.Fatal("expected an error for a sample missing lat/lng/altitude/battery")
	}
}

func TestPostFaultRequestShape(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "recorded"})
	}))
	defer srv.Close()

	c := New(srv.URL)
	fault := ingest.Fault{DroneID: "drone-004", Severity: ingest.SeverityError, Message: "ESC 3 overcurrent", RecordedAt: time.Now()}
	if err := c.PostFault(context.TODO(), fault); err != nil {
		t.Fatalf("PostFault: %v", err)
	}

	if gotPath != "/api/v1/fleet/drone-004/faults" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotBody["severity"] != "error" || gotBody["message"] != "ESC 3 overcurrent" {
		t.Fatalf("unexpected body: %+v", gotBody)
	}
}

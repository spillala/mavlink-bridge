// Package dronefleetclient posts Samples and Faults to the dronefleet API,
// matching the TelemetryUpdate/FaultReport contract in dronefleet's
// api/openapi.yaml exactly.
package dronefleetclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spillala/mavlink-bridge/internal/ingest"
)

// Client posts telemetry and fault events for one drone to the dronefleet API.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// New builds a Client against baseURL, e.g. "http://dronefleet-svc.dronefleet:80".
func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTPClient: &http.Client{Timeout: 10 * time.Second}}
}

type telemetryUpdate struct {
	Lat                 float64 `json:"lat"`
	Lng                 float64 `json:"lng"`
	Altitude            float64 `json:"altitude"`
	Battery             int     `json:"battery"`
	Armed               *bool   `json:"armed,omitempty"`
	LandedState         *string `json:"landedState,omitempty"`
	ExternalPower       *bool   `json:"externalPower,omitempty"`
	VoltageMv           *int    `json:"voltageMv,omitempty"`
	FlightMode          *string `json:"flightMode,omitempty"`
	SensorHealthBitmask *int64  `json:"sensorHealthBitmask,omitempty"`
	Source              string  `json:"source,omitempty"`
	RecordedAt          *string `json:"recordedAt,omitempty"`
}

type faultReport struct {
	Severity   string  `json:"severity"`
	Message    string  `json:"message"`
	RecordedAt *string `json:"recordedAt,omitempty"`
}

// PostSample sends a merged flight-state snapshot. The caller (Aggregator)
// guarantees Lat/Lng/Alt/BatteryPct are non-nil before this is called —
// dronefleet's TelemetryUpdate requires them.
func (c *Client) PostSample(ctx context.Context, s ingest.Sample) error {
	if s.Lat == nil || s.Lng == nil || s.Alt == nil || s.BatteryPct == nil {
		return fmt.Errorf("incomplete sample for %s: lat/lng/altitude/battery are required by dronefleet's API", s.DroneID)
	}
	landedState := string(s.LandedState)
	body := telemetryUpdate{
		Lat:                 *s.Lat,
		Lng:                 *s.Lng,
		Altitude:            *s.Alt,
		Battery:             *s.BatteryPct,
		Armed:               s.Armed,
		LandedState:         &landedState,
		ExternalPower:       s.ExternalPower,
		VoltageMv:           s.VoltageMv,
		FlightMode:          s.FlightMode,
		SensorHealthBitmask: s.SensorHealthBitmask,
		Source:              s.Source,
		RecordedAt:          rfc3339Ptr(s.RecordedAt),
	}
	return c.post(ctx, "/api/v1/fleet/"+s.DroneID+"/telemetry", body)
}

// PostFault sends one STATUSTEXT-derived fault event.
func (c *Client) PostFault(ctx context.Context, f ingest.Fault) error {
	body := faultReport{
		Severity:   string(f.Severity),
		Message:    f.Message,
		RecordedAt: rfc3339Ptr(f.RecordedAt),
	}
	return c.post(ctx, "/api/v1/fleet/"+f.DroneID+"/faults", body)
}

func (c *Client) post(ctx context.Context, path string, body any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s: unexpected status %d", path, resp.StatusCode)
	}
	return nil
}

func rfc3339Ptr(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339Nano)
	return &s
}

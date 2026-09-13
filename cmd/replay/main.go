// Command replay is the §11a test-data harness: it decodes a recorded
// MAVLink telemetry log (.tlog) and feeds it through the exact same
// ingest.Aggregator and dronefleetclient.Client the live bridge uses,
// preserving the log's own timestamps. That's what lets a messy real
// flight — dropped packets, a mid-flight heartbeat gap, GPS dropout — prove
// the ingestion path fails closed the same way it would live.
//
// Public PX4 flight logs are usually ULog (.ulg), not MAVLink .tlog — see
// the package doc on internal/mavlinksrc.ReplayFrames for the conversion
// gap this doesn't (yet) close.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/spillala/mavlink-bridge/internal/dronefleetclient"
	"github.com/spillala/mavlink-bridge/internal/ingest"
	"github.com/spillala/mavlink-bridge/internal/mavlinksrc"
)

func main() {
	var (
		file           = flag.String("file", "", "path to a .tlog file to replay (required)")
		dronefleetURL  = flag.String("dronefleet-url", envOr("DRONEFLEET_URL", "http://localhost:8080"), "base URL of the dronefleet API")
		droneID        = flag.String("drone-id", envOr("DRONE_ID", "drone-001"), "dronefleet drone ID to report as")
		systemID       = flag.Int("system-id", 1, "MAVLink SystemID to track within the log")
		heartbeatStale = flag.Duration("heartbeat-stale", 10*time.Second, "same fail-closed threshold as the live bridge, evaluated against the log's own timestamps")
	)
	flag.Parse()

	if *file == "" {
		log.Fatal("-file is required")
	}

	f, err := os.Open(*file)
	if err != nil {
		log.Fatalf("open %s: %v", *file, err)
	}
	defer f.Close()

	frames, err := mavlinksrc.ReplayFrames(f)
	if err != nil && len(frames) == 0 {
		log.Fatalf("decode %s: %v", *file, err)
	}
	if err != nil {
		log.Printf("[replay] %s ended early: %v (replaying %d frames decoded before that point)", *file, err, len(frames))
	}
	log.Printf("[replay] decoded %d frames from %s", len(frames), *file)

	ctx := context.Background()
	client := dronefleetclient.New(*dronefleetURL)

	var samplesPosted, faultsPosted, staleTransitions int
	agg := ingest.NewAggregator(*droneID, byte(*systemID),
		func(s ingest.Sample) {
			if err := client.PostSample(ctx, s); err != nil {
				log.Printf("[replay] post sample: %v", err)
				return
			}
			samplesPosted++
		},
		func(fault ingest.Fault) {
			log.Printf("[replay] fault %s/%s: %s", fault.DroneID, fault.Severity, fault.Message)
			if err := client.PostFault(ctx, fault); err != nil {
				log.Printf("[replay] post fault: %v", err)
				return
			}
			faultsPosted++
		},
	)

	for _, frm := range frames {
		// Check staleness against this frame's own recorded time, not wall
		// clock — this is what surfaces a mid-log heartbeat gap exactly as
		// it would happen live.
		if sample, stale := agg.CheckStale(frm.Time, *heartbeatStale); stale {
			log.Printf("[replay] heartbeat gap at %s: failing closed to UNKNOWN", frm.Time.Format(time.RFC3339))
			if err := client.PostSample(ctx, sample); err != nil {
				log.Printf("[replay] post stale sample: %v", err)
			} else {
				staleTransitions++
			}
		}
		agg.HandleFrame(frm)
	}

	log.Printf("[replay] done: %d samples posted, %d faults posted, %d heartbeat-gap transitions to UNKNOWN",
		samplesPosted, faultsPosted, staleTransitions)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

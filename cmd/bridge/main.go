// Command bridge is mavlink-bridge's live entry point: it connects to a
// PX4 SITL MAVLink stream over UDP, decodes flight state and STATUSTEXT
// fault causes, and reports both to the dronefleet API. Read-only — see
// internal/mavlinksrc for the boundary that keeps it that way.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/bluenviron/gomavlib/v3"

	"github.com/spillala/mavlink-bridge/internal/dronefleetclient"
	"github.com/spillala/mavlink-bridge/internal/health"
	"github.com/spillala/mavlink-bridge/internal/ingest"
	"github.com/spillala/mavlink-bridge/internal/mavlinksrc"
)

func main() {
	var (
		mavlinkAddress = flag.String("mavlink-address", envOr("MAVLINK_ADDRESS", "px4-sitl-gazebo-svc:14550"), "MAVLink UDP source, host:port")
		dronefleetURL  = flag.String("dronefleet-url", envOr("DRONEFLEET_URL", "http://localhost:8080"), "base URL of the dronefleet API")
		droneID        = flag.String("drone-id", envOr("DRONE_ID", "drone-001"), "dronefleet drone ID this MAVLink system reports as")
		systemID       = flag.Int("system-id", envOrInt("MAVLINK_SYSTEM_ID", 1), "MAVLink SystemID to track (PX4 SITL default: 1)")
		heartbeatStale = flag.Duration("heartbeat-stale", envOrDuration("HEARTBEAT_STALE", 10*time.Second), "how long without a HEARTBEAT before flight state fails closed to UNKNOWN")
		staleCheck     = flag.Duration("stale-check-interval", envOrDuration("STALE_CHECK_INTERVAL", 2*time.Second), "how often to check for a stale heartbeat")
		healthAddr     = flag.String("health-addr", envOr("HEALTH_ADDR", ""), "if set, serve /healthz and /readyz on this address, e.g. :8082")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var h *health.Server
	if *healthAddr != "" {
		h = health.New()
		go func() {
			if err := h.ListenAndServe(*healthAddr); err != nil {
				log.Fatalf("health server: %v", err)
			}
		}()
	}

	client := dronefleetclient.New(*dronefleetURL)

	agg := ingest.NewAggregator(*droneID, byte(*systemID),
		func(s ingest.Sample) {
			if err := client.PostSample(ctx, s); err != nil {
				log.Printf("[bridge] post sample for %s: %v", s.DroneID, err)
			}
		},
		func(f ingest.Fault) {
			log.Printf("[bridge] fault %s/%s: %s", f.DroneID, f.Severity, f.Message)
			if err := client.PostFault(ctx, f); err != nil {
				log.Printf("[bridge] post fault for %s: %v", f.DroneID, err)
			}
		},
	)

	live, err := mavlinksrc.Connect(*mavlinkAddress)
	if err != nil {
		log.Fatalf("connect to %s: %v", *mavlinkAddress, err)
	}
	defer live.Close()

	if h != nil {
		h.SetReady(true)
	}
	log.Printf("[bridge] connected to %s, tracking MAVLink system %d as %s", *mavlinkAddress, *systemID, *droneID)

	go func() {
		ticker := time.NewTicker(*staleCheck)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if sample, stale := agg.CheckStale(now, *heartbeatStale); stale {
					log.Printf("[bridge] heartbeat stale for %s, failing closed to UNKNOWN", *droneID)
					if err := client.PostSample(ctx, sample); err != nil {
						log.Printf("[bridge] post stale sample for %s: %v", sample.DroneID, err)
					}
				}
			}
		}
	}()

	onEvent := func(evt gomavlib.Event) {
		switch e := evt.(type) {
		case *gomavlib.EventParseError:
			// A real radio link drops and corrupts bytes; SITL over
			// localhost UDP essentially never does. Log and move on either
			// way — one bad frame must never take down the bridge.
			log.Printf("[bridge] parse error (skipping frame): %v", e.Error)
		case *gomavlib.EventChannelOpen:
			log.Printf("[bridge] channel opened: %v", e.Channel)
		case *gomavlib.EventChannelClose:
			log.Printf("[bridge] channel closed: %v (%v)", e.Channel, e.Error)
		}
	}

	for frame := range live.Frames(ctx, onEvent) {
		agg.HandleFrame(frame)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func envOrDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

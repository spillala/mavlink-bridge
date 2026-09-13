// Package health serves the liveness/readiness endpoints Kubernetes probes.
// Copied verbatim from dronefleet-agent/internal/health for consistency.
package health

import (
	"net/http"
	"sync/atomic"
)

// Server exposes /healthz (always OK once running) and /readyz (OK once
// the bridge has an open connection to the MAVLink source).
type Server struct {
	ready atomic.Bool
}

// New returns a Server that is not yet ready.
func New() *Server {
	return &Server{}
}

// SetReady flips readiness.
func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
}

// ListenAndServe blocks serving the health endpoints on addr.
func (s *Server) ListenAndServe(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.ready.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	return http.ListenAndServe(addr, mux)
}

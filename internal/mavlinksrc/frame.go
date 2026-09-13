// Package mavlinksrc decodes MAVLink frames from either a live UDP session
// (Live) or a recorded .tlog file (Replay) into a common Frame type, so
// internal/ingest never needs to know which one fed it. That's what lets
// the replay harness (§11a) exercise the exact same ingestion code path
// live SITL traffic does.
package mavlinksrc

import (
	"time"

	"github.com/bluenviron/gomavlib/v3/pkg/message"
)

// Frame is one decoded MAVLink message, independent of its transport.
type Frame struct {
	SystemID byte
	// Time is the frame's own timestamp: wall-clock receive time for Live,
	// the recorded timestamp for Replay. Ingest uses this as RecordedAt, so
	// replayed history keeps its original timing instead of collapsing to
	// "now".
	Time    time.Time
	Message message.Message
}

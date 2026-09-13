package mavlinksrc

import (
	"context"
	"fmt"
	"time"

	"github.com/bluenviron/gomavlib/v3"
	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"
)

// Live listens for a MAVLink source over UDP and emits decoded Frames.
//
// This binds a UDP *server* socket, not a client. PX4 SITL does not listen
// for GCS connections on 14550 — it sends its MAVLink stream outbound to
// 127.0.0.1:14550 (confirmed by packet capture against the live cluster's
// px4-sitl-gazebo pod: real MAVLink v2 frames arriving at that address,
// nothing ever bound to 14550 inside the PX4 container itself). That means
// this process can only receive that stream by sharing PX4's network
// namespace — deployed as a sidecar in the same Pod, not a separate
// Service/ClusterIP. See docs/decisions/0003-udp-server-sidecar-not-client.md.
//
// Read-only by construction regardless of topology: the only bytes this
// ever transmits are gomavlib's own periodic HEARTBEAT (MAV_TYPE_GCS,
// identity/presence only). Nothing in this package calls WriteMessageAll or
// WriteMessageTo, and nothing here can arm, disarm, change mode, or
// otherwise command the flight controller. See
// docs/decisions/0002-mavlink-library-and-read-only-boundary.md.
type Live struct {
	node *gomavlib.Node
}

// Connect binds a UDP server endpoint at address, e.g. ":14550" — run this
// as a sidecar sharing px4-sitl-gazebo's network namespace.
func Connect(address string) (*Live, error) {
	node := &gomavlib.Node{
		Endpoints: []gomavlib.EndpointConf{
			gomavlib.EndpointUDPServer{Address: address},
		},
		Dialect:     common.Dialect,
		OutVersion:  gomavlib.V2,
		OutSystemID: 250, // convention for a companion-computer/GCS-class peer, not a vehicle
	}
	if err := node.Initialize(); err != nil {
		return nil, fmt.Errorf("listen on %s: %w", address, err)
	}
	return &Live{node: node}, nil
}

// Close shuts down the connection.
func (l *Live) Close() {
	l.node.Close()
}

// Frames streams decoded frames until ctx is cancelled or the node closes.
// onEvent (optional) receives every raw gomavlib event, including
// EventParseError and channel open/close, for logging — corrupt or
// truncated MAVLink bytes on a real radio link show up here as parse
// errors and are skipped, never surfaced as a Frame.
func (l *Live) Frames(ctx context.Context, onEvent func(gomavlib.Event)) <-chan Frame {
	out := make(chan Frame)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-l.node.Events():
				if !ok {
					return
				}
				if onEvent != nil {
					onEvent(evt)
				}
				frm, ok := evt.(*gomavlib.EventFrame)
				if !ok {
					continue
				}
				select {
				case out <- Frame{SystemID: frm.SystemID(), Time: time.Now().UTC(), Message: frm.Message()}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

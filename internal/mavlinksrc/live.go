package mavlinksrc

import (
	"context"
	"fmt"
	"time"

	"github.com/bluenviron/gomavlib/v3"
	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"
)

// Live connects to a MAVLink source over UDP and emits decoded Frames.
//
// Read-only by construction: the only bytes this ever transmits are the
// node's own periodic HEARTBEAT — gomavlib sends this automatically
// (MAV_TYPE_GCS, identity/presence only) so PX4 recognises a GCS is
// listening and starts streaming to it; that handshake is what "UDP client"
// mode means for MAVLink. Nothing in this package calls WriteMessageAll or
// WriteMessageTo, and nothing here can arm, disarm, change mode, or
// otherwise command the flight controller. See
// docs/decisions/0002-mavlink-library-and-read-only-boundary.md.
type Live struct {
	node *gomavlib.Node
}

// Connect opens a UDP client endpoint at address, e.g. "px4-sitl-gazebo-svc:14550".
func Connect(address string) (*Live, error) {
	node := &gomavlib.Node{
		Endpoints: []gomavlib.EndpointConf{
			gomavlib.EndpointUDPClient{Address: address},
		},
		Dialect:     common.Dialect,
		OutVersion:  gomavlib.V2,
		OutSystemID: 250, // convention for a companion-computer/GCS-class peer, not a vehicle
	}
	if err := node.Initialize(); err != nil {
		return nil, fmt.Errorf("connect to %s: %w", address, err)
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

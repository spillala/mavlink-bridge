package mavlinksrc

import (
	"fmt"
	"io"

	"github.com/bluenviron/gomavlib/v3/pkg/dialect"
	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"
	"github.com/bluenviron/gomavlib/v3/pkg/tlog"
)

// ReplayFrames decodes every entry in a MAVLink telemetry log (.tlog — the
// format QGroundControl and MAVProxy record, and gomavlib itself can both
// read and write) and returns them as Frames carrying their *original*
// recorded timestamps, not replay time.
//
// This is the §11a replay harness: public PX4 flight logs are usually
// ULog (.ulg), which needs converting to MAVLink frames first (e.g. with
// pyulog or PX4's own log tools) before this function can read them — that
// conversion step is not implemented here. What this function guarantees is
// that once you have MAVLink bytes, real or replayed, they go through
// exactly the same ingest.Aggregator code path live SITL traffic does.
func ReplayFrames(r io.Reader) ([]Frame, error) {
	dialectRW := &dialect.ReadWriter{Dialect: common.Dialect}
	if err := dialectRW.Initialize(); err != nil {
		return nil, fmt.Errorf("initialize dialect: %w", err)
	}

	reader := &tlog.Reader{ByteReader: r, DialectRW: dialectRW}
	if err := reader.Initialize(); err != nil {
		return nil, fmt.Errorf("initialize tlog reader: %w", err)
	}

	var frames []Frame
	for {
		entry, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			// A corrupt or truncated entry ends the replay early rather than
			// panicking or fabricating data — same fail-closed spirit as a
			// live stream going silent.
			return frames, fmt.Errorf("read tlog entry %d: %w", len(frames), err)
		}
		frames = append(frames, Frame{
			SystemID: entry.Frame.GetSystemID(),
			Time:     entry.Time,
			Message:  entry.Frame.GetMessage(),
		})
	}
	return frames, nil
}

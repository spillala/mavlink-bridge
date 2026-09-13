package mavlinksrc

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bluenviron/gomavlib/v3/pkg/dialect"
	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"
	"github.com/bluenviron/gomavlib/v3/pkg/frame"
	"github.com/bluenviron/gomavlib/v3/pkg/message"
	"github.com/bluenviron/gomavlib/v3/pkg/tlog"
)

// testEntry is (timestamp, systemID, message) — the level a test author
// actually thinks at. writeTestLog does the checksum/encoding plumbing
// tlog.Writer.Write doesn't do for you (it writes pre-built Frames, and
// only auto-computes a checksum via the higher-level frame.Writer.WriteMessage
// path, which tlog.Writer doesn't use).
type testEntry struct {
	Time     time.Time
	SystemID byte
	Message  message.Message
}

func writeTestLog(t *testing.T, entries []testEntry) []byte {
	t.Helper()
	dialectRW := &dialect.ReadWriter{Dialect: common.Dialect}
	if err := dialectRW.Initialize(); err != nil {
		t.Fatalf("init dialect: %v", err)
	}

	var buf bytes.Buffer
	w := &tlog.Writer{ByteWriter: &buf, DialectRW: dialectRW}
	if err := w.Initialize(); err != nil {
		t.Fatalf("init tlog writer: %v", err)
	}

	for _, e := range entries {
		mp := dialectRW.GetMessage(e.Message.GetID())
		if mp == nil {
			t.Fatalf("message id %d not in dialect", e.Message.GetID())
		}
		raw := mp.Write(e.Message, true)

		fr := &frame.V2Frame{SystemID: e.SystemID, ComponentID: 1, Message: raw}
		fr.Checksum = fr.GenerateChecksum(mp.CRCExtra())

		if err := w.Write(&tlog.Entry{Time: e.Time, Frame: fr}); err != nil {
			t.Fatalf("write entry: %v", err)
		}
	}
	return buf.Bytes()
}

func TestReplayFramesRoundTrip(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(30 * time.Second)

	entries := []testEntry{
		{Time: t1, SystemID: 1, Message: &common.MessageHeartbeat{}},
		{Time: t2, SystemID: 1, Message: &common.MessageStatustext{Severity: common.MAV_SEVERITY_ERROR, Text: "ESC 3 overcurrent"}},
	}

	raw := writeTestLog(t, entries)

	frames, err := ReplayFrames(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReplayFrames: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(frames))
	}
	if !frames[0].Time.Equal(t1) || !frames[1].Time.Equal(t2) {
		t.Fatalf("expected original timestamps to be preserved, got %v and %v", frames[0].Time, frames[1].Time)
	}
	if _, ok := frames[0].Message.(*common.MessageHeartbeat); !ok {
		t.Fatalf("expected frame 0 to decode as MessageHeartbeat, got %T", frames[0].Message)
	}
	st, ok := frames[1].Message.(*common.MessageStatustext)
	if !ok || st.Text != "ESC 3 overcurrent" {
		t.Fatalf("expected frame 1 to decode as the STATUSTEXT sent, got %+v", frames[1].Message)
	}
}

func TestReplayFramesTruncatedLogReturnsPartialResultAndError(t *testing.T) {
	raw := writeTestLog(t, []testEntry{
		{Time: time.Now(), SystemID: 1, Message: &common.MessageHeartbeat{}},
	})

	// Truncate mid-entry to simulate a corrupt/incomplete log — §11a's
	// "corrupt or truncated messages" case. This must not panic, and must
	// still hand back whatever decoded cleanly before the truncation.
	truncated := raw[:len(raw)-3]

	frames, err := ReplayFrames(bytes.NewReader(truncated))
	if err == nil {
		t.Fatal("expected an error decoding a truncated log")
	}
	if len(frames) != 0 {
		t.Fatalf("expected 0 fully-decoded frames from a log truncated mid-entry, got %d", len(frames))
	}
}

func TestReplayFramesEmptyInput(t *testing.T) {
	frames, err := ReplayFrames(strings.NewReader(""))
	if err != nil {
		t.Fatalf("expected no error for an empty log, got %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("expected 0 frames, got %d", len(frames))
	}
}

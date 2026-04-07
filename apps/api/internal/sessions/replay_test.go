package sessions

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestReplayDataChunkFromPayload_AsciicastShape(t *testing.T) {
	payload := map[string]any{
		"data":       base64.StdEncoding.EncodeToString([]byte("ls -la\n")),
		"stream":     "stdout",
		"size":       7,
		"offset_sec": 1.25,
	}
	chunk, ok := replayChunkFromPayload(10, EventDataOut, time.Unix(1700000000, 0).UTC(), payload)
	if !ok {
		t.Fatalf("expected replay chunk")
	}
	if chunk.EventType != "output" {
		t.Fatalf("event_type = %q, want output", chunk.EventType)
	}
	if chunk.Direction != "out" {
		t.Fatalf("direction = %q, want out", chunk.Direction)
	}
	if chunk.Text != "ls -la\n" {
		t.Fatalf("text = %q", chunk.Text)
	}
	if chunk.OffsetSec != 1.25 {
		t.Fatalf("offset_sec = %v, want 1.25", chunk.OffsetSec)
	}
	if len(chunk.Asciicast) != 3 {
		t.Fatalf("expected asciicast tuple len 3")
	}
	if chunk.Asciicast[1] != "o" {
		t.Fatalf("asciicast code = %v, want o", chunk.Asciicast[1])
	}
}

func TestReplayResizeChunkFromPayload(t *testing.T) {
	payload := map[string]any{
		"cols":       120,
		"rows":       40,
		"offset_sec": 2.5,
	}
	chunk, ok := replayChunkFromPayload(22, EventTerminalResize, time.Unix(1700000100, 0).UTC(), payload)
	if !ok {
		t.Fatalf("expected resize chunk")
	}
	if chunk.EventType != "resize" {
		t.Fatalf("event_type = %q, want resize", chunk.EventType)
	}
	if chunk.Cols != 120 || chunk.Rows != 40 {
		t.Fatalf("cols/rows = %d/%d", chunk.Cols, chunk.Rows)
	}
	if len(chunk.Asciicast) != 3 {
		t.Fatalf("expected asciicast tuple len 3")
	}
	if chunk.Asciicast[1] != "r" {
		t.Fatalf("asciicast code = %v, want r", chunk.Asciicast[1])
	}
	if chunk.Asciicast[2] != "120x40" {
		t.Fatalf("resize payload = %v, want 120x40", chunk.Asciicast[2])
	}
}

func TestNormalizeReplayChunks_ComputesOffsetsAndDelays(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	chunks := []ReplayChunk{
		{EventID: 1, EventTime: base, EventType: "output", Text: "a"},
		{EventID: 2, EventTime: base.Add(150 * time.Millisecond), EventType: "output", Text: "b"},
		{EventID: 3, EventTime: base.Add(550 * time.Millisecond), EventType: "resize", Cols: 100, Rows: 30},
	}
	got := normalizeReplayChunks(chunks)
	if got[0].OffsetSec != 0 || got[0].DelaySec != 0 {
		t.Fatalf("first chunk offset/delay = %v/%v, want 0/0", got[0].OffsetSec, got[0].DelaySec)
	}
	if got[1].OffsetSec <= 0.14 || got[1].OffsetSec >= 0.16 {
		t.Fatalf("second offset = %v, want ~0.15", got[1].OffsetSec)
	}
	if got[1].DelaySec <= 0.14 || got[1].DelaySec >= 0.16 {
		t.Fatalf("second delay = %v, want ~0.15", got[1].DelaySec)
	}
	if got[2].DelaySec <= 0.39 || got[2].DelaySec >= 0.41 {
		t.Fatalf("third delay = %v, want ~0.40", got[2].DelaySec)
	}
	if got[2].Asciicast[1] != "r" {
		t.Fatalf("third asciicast code = %v, want r", got[2].Asciicast[1])
	}
}

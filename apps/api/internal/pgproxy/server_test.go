package pgproxy

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSessionsSvc and fakeCreds are not needed for unit tests that only
// exercise protocol parsing and message forwarding logic.

func TestParseQueryPayload(t *testing.T) {
	tests := []struct {
		name     string
		payload  []byte
		expected string
	}{
		{"simple query", []byte("SELECT 1\x00"), "SELECT 1"},
		{"empty", nil, ""},
		{"whitespace only", []byte("  \x00"), ""},
		{"no null term", []byte("SELECT 2"), "SELECT 2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseQueryPayload(tc.payload)
			if got != tc.expected {
				t.Errorf("got %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestParseParseMessage(t *testing.T) {
	tests := []struct {
		name      string
		payload   []byte
		wantName  string
		wantQuery string
	}{
		{
			"unnamed prepared",
			buildParsePayload("", "SELECT $1::int"),
			"",
			"SELECT $1::int",
		},
		{
			"named prepared",
			buildParsePayload("stmt1", "INSERT INTO t VALUES ($1)"),
			"stmt1",
			"INSERT INTO t VALUES ($1)",
		},
		{
			"empty payload",
			nil,
			"",
			"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name, query := parseParseMessage(tc.payload)
			if name != tc.wantName {
				t.Errorf("name: got %q, want %q", name, tc.wantName)
			}
			if query != tc.wantQuery {
				t.Errorf("query: got %q, want %q", query, tc.wantQuery)
			}
		})
	}
}

func TestParseExecuteMessage(t *testing.T) {
	tests := []struct {
		name     string
		payload  []byte
		wantName string
	}{
		{"unnamed portal", buildExecutePayload("", 0), ""},
		{"named portal", buildExecutePayload("portal1", 100), "portal1"},
		{"empty", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseExecuteMessage(tc.payload)
			if got != tc.wantName {
				t.Errorf("got %q, want %q", got, tc.wantName)
			}
		})
	}
}

func TestParseCloseMessage(t *testing.T) {
	t.Run("close statement", func(t *testing.T) {
		payload := append([]byte{'S'}, []byte("stmt1\x00")...)
		target, name := parseCloseMessage(payload)
		if target != 'S' || name != "stmt1" {
			t.Errorf("got target=%c name=%q, want S stmt1", target, name)
		}
	})
	t.Run("close portal", func(t *testing.T) {
		payload := append([]byte{'P'}, []byte("portal1\x00")...)
		target, name := parseCloseMessage(payload)
		if target != 'P' || name != "portal1" {
			t.Errorf("got target=%c name=%q, want P portal1", target, name)
		}
	})
	t.Run("empty", func(t *testing.T) {
		target, name := parseCloseMessage(nil)
		if target != 0 || name != "" {
			t.Errorf("got target=%c name=%q, want 0 empty", target, name)
		}
	})
}

func TestPreparedStmtCache(t *testing.T) {
	c := newPreparedStmtCache()

	// Store and lookup.
	c.Store("s1", "SELECT 1")
	q, ok := c.Lookup("s1")
	if !ok || q != "SELECT 1" {
		t.Fatalf("got %q ok=%v, want SELECT 1", q, ok)
	}

	// Overwrite.
	c.Store("s1", "SELECT 2")
	q, ok = c.Lookup("s1")
	if !ok || q != "SELECT 2" {
		t.Fatalf("got %q ok=%v after overwrite", q, ok)
	}

	// Unknown.
	_, ok = c.Lookup("unknown")
	if ok {
		t.Fatal("expected not found for unknown")
	}

	// Delete.
	c.Delete("s1")
	_, ok = c.Lookup("s1")
	if ok {
		t.Fatal("expected not found after delete")
	}
}

// TestSimpleQueryLogging verifies that simple Q messages are captured by
// forwardClientMessages.
func TestSimpleQueryLogging(t *testing.T) {
	events := &eventCollector{}
	svc := &Service{
		cfg:        Config{QueryMaxBytes: 4096},
		logger:     discardLogger(),
		queryLogCh: make(chan queryLogEvent, 16),
	}

	// Build client input as a buffer.
	var clientBuf bytes.Buffer
	writeTestFrontendMessage(&clientBuf, 'Q', []byte("SELECT 42\x00"))
	writeTestFrontendMessage(&clientBuf, 'X', nil)

	// Upstream is just a discard writer.
	reg := SessionRegistration{SessionID: "s1", UserID: "u1", AssetID: "a1"}
	err := svc.forwardClientMessages(reg, &clientBuf, io.Discard)
	if err != nil {
		t.Fatalf("forwardClientMessages: %v", err)
	}

	close(svc.queryLogCh)
	for evt := range svc.queryLogCh {
		events.add(evt)
	}

	if len(events.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events.events))
	}
	evt := events.events[0]
	if evt.Query != "SELECT 42" {
		t.Errorf("query: got %q, want SELECT 42", evt.Query)
	}
	if evt.ProtocolType != "simple" {
		t.Errorf("protocol_type: got %q, want simple", evt.ProtocolType)
	}
	if evt.Prepared {
		t.Error("prepared should be false for simple query")
	}
}

// TestExtendedProtocolLogging verifies Parse->Execute logs correctly.
func TestExtendedProtocolLogging(t *testing.T) {
	svc := &Service{
		cfg:        Config{QueryMaxBytes: 4096},
		logger:     discardLogger(),
		queryLogCh: make(chan queryLogEvent, 16),
	}

	var clientBuf bytes.Buffer
	writeTestFrontendMessage(&clientBuf, 'P', buildParsePayload("s1", "SELECT $1::int"))
	writeTestFrontendMessage(&clientBuf, 'B', []byte("\x00\x00\x00\x00\x00\x00"))
	writeTestFrontendMessage(&clientBuf, 'E', buildExecutePayload("s1", 0))
	writeTestFrontendMessage(&clientBuf, 'S', nil)
	writeTestFrontendMessage(&clientBuf, 'X', nil)

	reg := SessionRegistration{SessionID: "s2", UserID: "u1", AssetID: "a1"}
	err := svc.forwardClientMessages(reg, &clientBuf, io.Discard)
	if err != nil {
		t.Fatalf("forwardClientMessages: %v", err)
	}

	close(svc.queryLogCh)
	var events []queryLogEvent
	for evt := range svc.queryLogCh {
		events = append(events, evt)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Query != "SELECT $1::int" {
		t.Errorf("query: got %q", events[0].Query)
	}
	if events[0].ProtocolType != "extended" {
		t.Errorf("protocol_type: got %q, want extended", events[0].ProtocolType)
	}
	if !events[0].Prepared {
		t.Error("prepared should be true for named statement")
	}
}

// TestPreparedStatementReuse verifies multiple executes of the same statement.
func TestPreparedStatementReuse(t *testing.T) {
	svc := &Service{
		cfg:        Config{QueryMaxBytes: 4096},
		logger:     discardLogger(),
		queryLogCh: make(chan queryLogEvent, 16),
	}

	var clientBuf bytes.Buffer
	writeTestFrontendMessage(&clientBuf, 'P', buildParsePayload("s1", "UPDATE t SET x=$1"))
	writeTestFrontendMessage(&clientBuf, 'E', buildExecutePayload("s1", 0))
	writeTestFrontendMessage(&clientBuf, 'E', buildExecutePayload("s1", 0))
	writeTestFrontendMessage(&clientBuf, 'E', buildExecutePayload("s1", 0))
	writeTestFrontendMessage(&clientBuf, 'X', nil)

	reg := SessionRegistration{SessionID: "s3", UserID: "u1", AssetID: "a1"}
	_ = svc.forwardClientMessages(reg, &clientBuf, io.Discard)

	close(svc.queryLogCh)
	var events []queryLogEvent
	for evt := range svc.queryLogCh {
		events = append(events, evt)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	for i, evt := range events {
		if evt.Query != "UPDATE t SET x=$1" {
			t.Errorf("event %d: got %q", i, evt.Query)
		}
	}
}

func TestParseServerFirst(t *testing.T) {
	salt := base64.StdEncoding.EncodeToString([]byte("saltsalt"))
	msg := "r=clientnonceservernonce,s=" + salt + ",i=4096"
	nonce, saltBytes, iters, err := parseServerFirst(msg)
	if err != nil {
		t.Fatal(err)
	}
	if nonce != "clientnonceservernonce" {
		t.Errorf("nonce: %q", nonce)
	}
	if string(saltBytes) != "saltsalt" {
		t.Errorf("salt: %q", saltBytes)
	}
	if iters != 4096 {
		t.Errorf("iterations: %d", iters)
	}
}

func TestSCRAMHelpers(t *testing.T) {
	// Verify scramSaslName escaping.
	if got := scramSaslName("user=1,2"); got != "user=3D1=2C2" {
		t.Errorf("saslName: %q", got)
	}

	// Verify Hi produces deterministic output.
	result := scramHi([]byte("password"), []byte("salt"), 1)
	if len(result) != 32 {
		t.Errorf("Hi output length: %d", len(result))
	}

	// Verify HMAC produces correct length.
	mac := scramHMAC([]byte("key"), []byte("data"))
	if len(mac) != 32 {
		t.Errorf("HMAC output length: %d", len(mac))
	}
}

func TestBuildSASLInitialResponse(t *testing.T) {
	data := []byte("n,,n=user,r=nonce123")
	resp := buildSASLInitialResponse("SCRAM-SHA-256", data)

	// Should be: mechanism\0 + int32(len) + data
	idx := bytes.IndexByte(resp, 0)
	if idx < 0 {
		t.Fatal("no null terminator")
	}
	mech := string(resp[:idx])
	if mech != "SCRAM-SHA-256" {
		t.Errorf("mechanism: %q", mech)
	}
	lenBytes := resp[idx+1 : idx+5]
	dataLen := binary.BigEndian.Uint32(lenBytes)
	if int(dataLen) != len(data) {
		t.Errorf("data length: got %d, want %d", dataLen, len(data))
	}
	if !bytes.Equal(resp[idx+5:], data) {
		t.Error("data mismatch")
	}
}

// --- test helpers ---

type eventCollector struct {
	mu     sync.Mutex
	events []queryLogEvent
}

func (c *eventCollector) add(evt queryLogEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, evt)
}

func writeTestFrontendMessage(w io.Writer, msgType byte, payload []byte) {
	w.Write([]byte{msgType})
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(payload)+4))
	w.Write(lenBuf)
	if len(payload) > 0 {
		w.Write(payload)
	}
}

func buildParsePayload(name, query string) []byte {
	var buf bytes.Buffer
	buf.WriteString(name)
	buf.WriteByte(0)
	buf.WriteString(query)
	buf.WriteByte(0)
	// int16 param count = 0
	buf.Write([]byte{0, 0})
	return buf.Bytes()
}

func buildExecutePayload(portal string, maxRows int32) []byte {
	var buf bytes.Buffer
	buf.WriteString(portal)
	buf.WriteByte(0)
	rowBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(rowBuf, uint32(maxRows))
	buf.Write(rowBuf)
	return buf.Bytes()
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Silence unused import warnings.
var _ = time.Now
var _ = strings.TrimSpace

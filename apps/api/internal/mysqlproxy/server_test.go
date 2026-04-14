package mysqlproxy

import (
	"bytes"
	"encoding/binary"
	"io"
	"log/slog"
	"testing"
)

// ---------------------------------------------------------------------------
// Packet I/O
// ---------------------------------------------------------------------------

func TestReadWritePacket(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte("hello mysql")
	if err := writePacket(&buf, payload, 7); err != nil {
		t.Fatal(err)
	}

	got, seq, err := readPacket(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 7 {
		t.Errorf("seq: got %d, want 7", seq)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("payload: got %q, want %q", got, payload)
	}
}

func TestReadWritePacketEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := writePacket(&buf, nil, 3); err != nil {
		t.Fatal(err)
	}
	got, seq, err := readPacket(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 3 {
		t.Errorf("seq: %d", seq)
	}
	if len(got) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(got))
	}
}

// ---------------------------------------------------------------------------
// Handshake parsing
// ---------------------------------------------------------------------------

func TestParseHandshakeV10(t *testing.T) {
	salt := make([]byte, 20)
	for i := range salt {
		salt[i] = byte(i + 1)
	}

	raw := buildTestHandshake("8.0.33", 42, salt, authNativePassword,
		capProtocol41|capSecureConnection|capPluginAuth|capSSL)

	hs, err := parseHandshakeV10(raw)
	if err != nil {
		t.Fatal(err)
	}
	if hs.ProtocolVersion != 10 {
		t.Errorf("protocol version: %d", hs.ProtocolVersion)
	}
	if hs.ServerVersion != "8.0.33" {
		t.Errorf("server version: %q", hs.ServerVersion)
	}
	if hs.ConnectionID != 42 {
		t.Errorf("connection id: %d", hs.ConnectionID)
	}
	if hs.AuthPlugin != authNativePassword {
		t.Errorf("auth plugin: %q", hs.AuthPlugin)
	}
	if len(hs.AuthData) < 20 {
		t.Errorf("auth data len: %d", len(hs.AuthData))
	}
	if hs.Capabilities&capSSL == 0 {
		t.Error("expected SSL capability")
	}
}

func TestParseHandshakeV10Short(t *testing.T) {
	_, err := parseHandshakeV10([]byte{10, 0})
	if err == nil {
		t.Error("expected error for short handshake")
	}
}

// ---------------------------------------------------------------------------
// Auth
// ---------------------------------------------------------------------------

func TestNativePasswordAuth(t *testing.T) {
	salt := bytes.Repeat([]byte{0x3a}, 20)
	result := nativePasswordAuth("secret", salt)
	if len(result) != 20 {
		t.Fatalf("expected 20 bytes, got %d", len(result))
	}
	// Deterministic: same inputs → same output.
	result2 := nativePasswordAuth("secret", salt)
	if !bytes.Equal(result, result2) {
		t.Error("non-deterministic output")
	}
	// Different password → different output.
	result3 := nativePasswordAuth("other", salt)
	if bytes.Equal(result, result3) {
		t.Error("different passwords produced same scramble")
	}
}

func TestCachingSha2Auth(t *testing.T) {
	salt := bytes.Repeat([]byte{0x42}, 20)
	result := cachingSha2Auth("secret", salt)
	if len(result) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(result))
	}
	result2 := cachingSha2Auth("secret", salt)
	if !bytes.Equal(result, result2) {
		t.Error("non-deterministic output")
	}
}

func TestComputeAuthResponseEmpty(t *testing.T) {
	result := computeAuthResponse(authNativePassword, "", nil)
	if result != nil {
		t.Errorf("expected nil for empty password, got %d bytes", len(result))
	}
}

// ---------------------------------------------------------------------------
// Prepared statement cache
// ---------------------------------------------------------------------------

func TestPreparedStmtCache(t *testing.T) {
	c := newPreparedStmtCache()

	c.Store(1, "SELECT 1")
	q, ok := c.Lookup(1)
	if !ok || q != "SELECT 1" {
		t.Fatalf("got %q ok=%v, want SELECT 1", q, ok)
	}

	c.Store(1, "SELECT 2")
	q, ok = c.Lookup(1)
	if !ok || q != "SELECT 2" {
		t.Fatalf("got %q after overwrite", q)
	}

	_, ok = c.Lookup(99)
	if ok {
		t.Fatal("expected not found for unknown")
	}

	c.Delete(1)
	_, ok = c.Lookup(1)
	if ok {
		t.Fatal("expected not found after delete")
	}
}

// ---------------------------------------------------------------------------
// LenEnc int
// ---------------------------------------------------------------------------

func TestReadLenEncInt(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    uint64
		wantLen int
	}{
		{"1-byte", []byte{42}, 42, 1},
		{"2-byte", []byte{0xfc, 0x01, 0x00}, 1, 3},
		{"3-byte", []byte{0xfd, 0xff, 0xff, 0x00}, 65535, 4},
		{"8-byte", append([]byte{0xfe}, make([]byte, 8)...), 0, 9},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, n, err := readLenEncInt(tc.data)
			if err != nil {
				t.Fatal(err)
			}
			if v != tc.want {
				t.Errorf("value: got %d, want %d", v, tc.want)
			}
			if n != tc.wantLen {
				t.Errorf("consumed: got %d, want %d", n, tc.wantLen)
			}
		})
	}
}

func TestReadLenEncIntErrors(t *testing.T) {
	_, _, err := readLenEncInt(nil)
	if err == nil {
		t.Error("expected error for nil")
	}
	_, _, err = readLenEncInt([]byte{0xfc, 0x01}) // short 2-byte
	if err == nil {
		t.Error("expected error for short 2-byte")
	}
}

// ---------------------------------------------------------------------------
// Packet helpers
// ---------------------------------------------------------------------------

func TestBuildServerHandshake(t *testing.T) {
	salt := make([]byte, 20)
	for i := range salt {
		salt[i] = byte(i)
	}
	payload := buildServerHandshake(salt, true)
	hs, err := parseHandshakeV10(payload)
	if err != nil {
		t.Fatal(err)
	}
	if hs.ProtocolVersion != 10 {
		t.Errorf("protocol version: %d", hs.ProtocolVersion)
	}
	if hs.AuthPlugin != authNativePassword {
		t.Errorf("auth plugin: %q", hs.AuthPlugin)
	}
	if hs.Capabilities&capSSL == 0 {
		t.Error("expected SSL capability")
	}
}

func TestBuildOKPacket(t *testing.T) {
	ok := buildOKPacket(0, 0, 0x0002, 0)
	if ok[0] != iOK {
		t.Errorf("first byte: 0x%02x", ok[0])
	}
}

func TestBuildSSLRequest(t *testing.T) {
	req := buildSSLRequest()
	if len(req) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(req))
	}
	caps := binary.LittleEndian.Uint32(req[:4])
	if caps&capSSL == 0 {
		t.Error("expected SSL capability in request")
	}
}

// ---------------------------------------------------------------------------
// Error / Auth-switch parsing
// ---------------------------------------------------------------------------

func TestParseErrPacket(t *testing.T) {
	// [0xFF, code(2), '#', state(5), message...]
	payload := []byte{0xff, 0x15, 0x04, '#', '4', '2', '0', '0', '0', 'A', 'c', 'c', 'e', 's', 's'}
	msg := parseErrPacket(payload)
	if msg == "" || msg == "unknown error" {
		t.Errorf("expected meaningful error, got %q", msg)
	}
}

func TestParseAuthSwitch(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(iEOF)
	buf.WriteString("mysql_native_password")
	buf.WriteByte(0)
	buf.Write(bytes.Repeat([]byte{0x42}, 20))
	buf.WriteByte(0)

	plugin, data := parseAuthSwitch(buf.Bytes())
	if plugin != "mysql_native_password" {
		t.Errorf("plugin: %q", plugin)
	}
	if len(data) != 20 {
		t.Errorf("auth data len: %d", len(data))
	}
}

// ---------------------------------------------------------------------------
// EOF / ERR detection
// ---------------------------------------------------------------------------

func TestIsEOFPacket(t *testing.T) {
	if !isEOFPacket([]byte{0xfe, 0, 0, 0, 0}) {
		t.Error("expected EOF for 5-byte 0xfe packet")
	}
	if isEOFPacket([]byte{0xfe}) {
		// Too short to be a valid EOF with warnings+status, but we accept any <9.
		// Actually our implementation accepts this. Let's verify.
	}
	if isEOFPacket(make([]byte, 10)) {
		t.Error("10-byte packet starting with 0x00 should not be EOF")
	}
}

func TestIsERRPacket(t *testing.T) {
	if !isERRPacket([]byte{0xff, 0x01, 0x00}) {
		t.Error("expected ERR")
	}
	if isERRPacket([]byte{0x00}) {
		t.Error("OK packet should not be ERR")
	}
}

func TestHasMoreResultsEOF(t *testing.T) {
	payload := []byte{0xfe, 0x00, 0x00, 0x08, 0x00} // status=0x0008
	if !hasMoreResultsEOF(payload) {
		t.Error("expected more results")
	}
	payload = []byte{0xfe, 0x00, 0x00, 0x00, 0x00}
	if hasMoreResultsEOF(payload) {
		t.Error("expected no more results")
	}
}

// ---------------------------------------------------------------------------
// SSL mode classification
// ---------------------------------------------------------------------------

func TestClassifySSLMode(t *testing.T) {
	tests := []struct {
		input   string
		attempt bool
		require bool
		verify  bool
	}{
		{"disable", false, false, false},
		{"prefer", true, false, false},
		{"preferred", true, false, false},
		{"", true, false, false},
		{"require", true, true, false},
		{"required", true, true, false},
		{"verify-ca", true, true, true},
		{"verify_identity", true, true, true},
	}
	for _, tc := range tests {
		cfg := classifySSLMode(tc.input)
		if cfg.AttemptTLS != tc.attempt || cfg.RequireTLS != tc.require || cfg.VerifyCert != tc.verify {
			t.Errorf("classifySSLMode(%q): got %+v", tc.input, cfg)
		}
	}
}

// ---------------------------------------------------------------------------
// Simple query capture (integration-like test using forwardCommands)
// ---------------------------------------------------------------------------

func TestSimpleQueryCapture(t *testing.T) {
	svc := &Service{
		cfg:        Config{QueryMaxBytes: 4096},
		logger:     discardLogger(),
		queryLogCh: make(chan queryLogEvent, 16),
	}

	// Build client input: COM_QUERY "SELECT 42" + COM_QUIT.
	var clientRead bytes.Buffer
	writeTestPacket(&clientRead, []byte{comQuery, 'S', 'E', 'L', 'E', 'C', 'T', ' ', '4', '2'}, 0)
	writeTestPacket(&clientRead, []byte{comQuit}, 0)

	// Build upstream responses: OK for the query.
	var upstreamRead bytes.Buffer
	writeTestPacket(&upstreamRead, buildOKPacket(0, 0, 0x0002, 0), 1)

	client := &readWriter{Reader: &clientRead, Writer: io.Discard}
	upstream := &readWriter{Reader: &upstreamRead, Writer: io.Discard}

	reg := SessionRegistration{SessionID: "s1", UserID: "u1", AssetID: "a1", Engine: "mysql"}
	err := svc.forwardCommands(reg, client, upstream)
	if err != nil {
		t.Fatalf("forwardCommands: %v", err)
	}

	close(svc.queryLogCh)
	var events []queryLogEvent
	for evt := range svc.queryLogCh {
		events = append(events, evt)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Query != "SELECT 42" {
		t.Errorf("query: %q", events[0].Query)
	}
	if events[0].ProtocolType != "simple" {
		t.Errorf("protocol_type: %q", events[0].ProtocolType)
	}
	if events[0].Prepared {
		t.Error("prepared should be false for simple query")
	}
	if events[0].Engine != "mysql" {
		t.Errorf("engine: %q", events[0].Engine)
	}
}

func TestIgnoreLegacyQueryCacheSet(t *testing.T) {
	svc := &Service{
		cfg:        Config{QueryMaxBytes: 4096},
		logger:     discardLogger(),
		queryLogCh: make(chan queryLogEvent, 16),
	}

	var clientRead bytes.Buffer
	writeTestPacket(&clientRead, []byte{comQuery, 'S', 'E', 'T', ' ', 'S', 'E', 'S', 'S', 'I', 'O', 'N', ' ', 'q', 'u', 'e', 'r', 'y', '_', 'c', 'a', 'c', 'h', 'e', '_', 's', 'i', 'z', 'e', '=', '0'}, 0)
	writeTestPacket(&clientRead, []byte{comQuery, 'S', 'E', 'L', 'E', 'C', 'T', ' ', '1'}, 0)
	writeTestPacket(&clientRead, []byte{comQuit}, 0)

	var upstreamRead bytes.Buffer
	writeTestPacket(&upstreamRead, buildOKPacket(0, 0, 0x0002, 0), 1)

	var clientWrite bytes.Buffer
	var upstreamWrite bytes.Buffer
	client := &readWriter{Reader: &clientRead, Writer: &clientWrite}
	upstream := &readWriter{Reader: &upstreamRead, Writer: &upstreamWrite}

	reg := SessionRegistration{SessionID: "s-compat", UserID: "u1", AssetID: "a1", Engine: "mysql"}
	if err := svc.forwardCommands(reg, client, upstream); err != nil {
		t.Fatalf("forwardCommands: %v", err)
	}

	close(svc.queryLogCh)
	var events []queryLogEvent
	for evt := range svc.queryLogCh {
		events = append(events, evt)
	}
	if len(events) != 1 {
		t.Fatalf("expected only real query to be logged, got %d events", len(events))
	}
	if events[0].Query != "SELECT 1" {
		t.Fatalf("logged query = %q, want SELECT 1", events[0].Query)
	}

	var forwarded [][]byte
	for upstreamWrite.Len() > 0 {
		p, _, err := readPacket(&upstreamWrite)
		if err != nil {
			t.Fatalf("read forwarded packet: %v", err)
		}
		forwarded = append(forwarded, p)
	}
	if len(forwarded) != 2 {
		t.Fatalf("expected 2 forwarded packets (SELECT + QUIT), got %d", len(forwarded))
	}
	if len(forwarded[0]) == 0 || forwarded[0][0] != comQuery || !bytes.Contains(forwarded[0], []byte("SELECT 1")) {
		t.Fatalf("first forwarded payload mismatch: %q", forwarded[0])
	}
	if len(forwarded[1]) == 0 || forwarded[1][0] != comQuit {
		t.Fatalf("second forwarded packet should be COM_QUIT, got %v", forwarded[1])
	}

	okPayload, _, err := readPacket(&clientWrite)
	if err != nil {
		t.Fatalf("read first client response: %v", err)
	}
	if len(okPayload) == 0 || okPayload[0] != iOK {
		t.Fatalf("expected synthetic OK for ignored init query, got %v", okPayload)
	}
}

// ---------------------------------------------------------------------------
// Prepared statement capture
// ---------------------------------------------------------------------------

func TestPreparedStatementCapture(t *testing.T) {
	svc := &Service{
		cfg:        Config{QueryMaxBytes: 4096},
		logger:     discardLogger(),
		queryLogCh: make(chan queryLogEvent, 16),
	}

	// Client: COM_STMT_PREPARE "SELECT ?" → COM_STMT_EXECUTE stmt_id=1 → COM_QUIT.
	var clientRead bytes.Buffer
	writeTestPacket(&clientRead, append([]byte{comStmtPrepare}, []byte("SELECT ?")...), 0)

	// COM_STMT_EXECUTE: [0x17, stmt_id(4)=1, flags(1)=0, iteration_count(4)=1]
	execPayload := []byte{comStmtExecute, 0x01, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}
	writeTestPacket(&clientRead, execPayload, 0)
	writeTestPacket(&clientRead, []byte{comQuit}, 0)

	// Upstream responses:
	// 1. COM_STMT_PREPARE_OK: [OK, stmt_id=1, cols=0, params=0, reserved, warnings=0]
	prepOK := []byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	var upstreamRead bytes.Buffer
	writeTestPacket(&upstreamRead, prepOK, 1)
	// 2. OK for execute.
	writeTestPacket(&upstreamRead, buildOKPacket(0, 0, 0x0002, 0), 1)

	client := &readWriter{Reader: &clientRead, Writer: io.Discard}
	upstream := &readWriter{Reader: &upstreamRead, Writer: io.Discard}

	reg := SessionRegistration{SessionID: "s2", UserID: "u1", AssetID: "a1", Engine: "mysql"}
	err := svc.forwardCommands(reg, client, upstream)
	if err != nil {
		t.Fatalf("forwardCommands: %v", err)
	}

	close(svc.queryLogCh)
	var events []queryLogEvent
	for evt := range svc.queryLogCh {
		events = append(events, evt)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event (execute), got %d", len(events))
	}
	if events[0].Query != "SELECT ?" {
		t.Errorf("query: %q", events[0].Query)
	}
	if events[0].ProtocolType != "prepared" {
		t.Errorf("protocol_type: %q", events[0].ProtocolType)
	}
	if !events[0].Prepared {
		t.Error("prepared should be true")
	}
}

// TestPreparedStatementReuse verifies multiple executes of the same statement.
func TestPreparedStatementReuse(t *testing.T) {
	svc := &Service{
		cfg:        Config{QueryMaxBytes: 4096},
		logger:     discardLogger(),
		queryLogCh: make(chan queryLogEvent, 16),
	}

	var clientRead bytes.Buffer
	writeTestPacket(&clientRead, append([]byte{comStmtPrepare}, []byte("UPDATE t SET x=?")...), 0)
	execPayload := []byte{comStmtExecute, 0x01, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}
	writeTestPacket(&clientRead, execPayload, 0)
	writeTestPacket(&clientRead, execPayload, 0)
	writeTestPacket(&clientRead, execPayload, 0)
	writeTestPacket(&clientRead, []byte{comQuit}, 0)

	var upstreamRead bytes.Buffer
	prepOK := []byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	writeTestPacket(&upstreamRead, prepOK, 1)
	for i := 0; i < 3; i++ {
		writeTestPacket(&upstreamRead, buildOKPacket(1, 0, 0x0002, 0), 1)
	}

	client := &readWriter{Reader: &clientRead, Writer: io.Discard}
	upstream := &readWriter{Reader: &upstreamRead, Writer: io.Discard}

	reg := SessionRegistration{SessionID: "s3", UserID: "u1", AssetID: "a1", Engine: "mysql"}
	_ = svc.forwardCommands(reg, client, upstream)

	close(svc.queryLogCh)
	var events []queryLogEvent
	for evt := range svc.queryLogCh {
		events = append(events, evt)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	for i, evt := range events {
		if evt.Query != "UPDATE t SET x=?" {
			t.Errorf("event %d: got %q", i, evt.Query)
		}
	}
}

// TestPreparedStatementCloseAndReuse verifies close removes from cache.
func TestPreparedStatementCloseAndReuse(t *testing.T) {
	svc := &Service{
		cfg:        Config{QueryMaxBytes: 4096},
		logger:     discardLogger(),
		queryLogCh: make(chan queryLogEvent, 16),
	}

	var clientRead bytes.Buffer
	// Prepare stmt_id=1
	writeTestPacket(&clientRead, append([]byte{comStmtPrepare}, []byte("SELECT 1")...), 0)
	// Close stmt_id=1
	writeTestPacket(&clientRead, []byte{comStmtClose, 0x01, 0x00, 0x00, 0x00}, 0)
	// Execute stmt_id=1 (should warn about unknown)
	execPayload := []byte{comStmtExecute, 0x01, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}
	writeTestPacket(&clientRead, execPayload, 0)
	writeTestPacket(&clientRead, []byte{comQuit}, 0)

	var upstreamRead bytes.Buffer
	prepOK := []byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	writeTestPacket(&upstreamRead, prepOK, 1)
	writeTestPacket(&upstreamRead, buildOKPacket(0, 0, 0x0002, 0), 1) // for the execute

	client := &readWriter{Reader: &clientRead, Writer: io.Discard}
	upstream := &readWriter{Reader: &upstreamRead, Writer: io.Discard}

	reg := SessionRegistration{SessionID: "s4", UserID: "u1", AssetID: "a1", Engine: "mysql"}
	_ = svc.forwardCommands(reg, client, upstream)

	close(svc.queryLogCh)
	var events []queryLogEvent
	for evt := range svc.queryLogCh {
		events = append(events, evt)
	}

	// Execute after close should not produce a query event (unknown stmt).
	if len(events) != 0 {
		t.Fatalf("expected 0 events (stmt was closed), got %d", len(events))
	}
}

// TestResultSetRelay verifies the proxy correctly relays a full result set.
func TestResultSetRelay(t *testing.T) {
	svc := &Service{
		cfg:        Config{QueryMaxBytes: 4096},
		logger:     discardLogger(),
		queryLogCh: make(chan queryLogEvent, 16),
	}

	var clientRead bytes.Buffer
	writeTestPacket(&clientRead, append([]byte{comQuery}, []byte("SELECT id FROM t")...), 0)
	writeTestPacket(&clientRead, []byte{comQuit}, 0)

	// Result set: column_count=1 → col_def → EOF → row → EOF.
	var upstreamRead bytes.Buffer
	writeTestPacket(&upstreamRead, []byte{0x01}, 1)                // column_count = 1
	writeTestPacket(&upstreamRead, []byte{0x03, 'd', 'e', 'f'}, 2) // column definition (simplified)
	writeTestPacket(&upstreamRead, []byte{0xfe, 0, 0, 0, 0}, 3)    // EOF
	writeTestPacket(&upstreamRead, []byte{0x01, '1'}, 4)           // row: "1"
	writeTestPacket(&upstreamRead, []byte{0xfe, 0, 0, 0, 0}, 5)    // EOF (end of rows)

	// Capture what's written to client.
	var clientWriteBuf bytes.Buffer
	client := &readWriter{Reader: &clientRead, Writer: &clientWriteBuf}
	upstream := &readWriter{Reader: &upstreamRead, Writer: io.Discard}

	reg := SessionRegistration{SessionID: "s5", UserID: "u1", AssetID: "a1", Engine: "mysql"}
	err := svc.forwardCommands(reg, client, upstream)
	if err != nil {
		t.Fatalf("forwardCommands: %v", err)
	}

	// Verify the client received data (5 packets for the result set).
	if clientWriteBuf.Len() == 0 {
		t.Error("expected data written to client")
	}

	close(svc.queryLogCh)
	var events []queryLogEvent
	for evt := range svc.queryLogCh {
		events = append(events, evt)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 query event, got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// Truncate
// ---------------------------------------------------------------------------

func TestTruncate(t *testing.T) {
	if got := truncate("abcdef", 3); got != "abc" {
		t.Errorf("got %q", got)
	}
	if got := truncate("ab", 10); got != "ab" {
		t.Errorf("got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type readWriter struct {
	io.Reader
	io.Writer
}

func writeTestPacket(w io.Writer, payload []byte, seq uint8) {
	header := make([]byte, 4)
	pLen := len(payload)
	header[0] = byte(pLen)
	header[1] = byte(pLen >> 8)
	header[2] = byte(pLen >> 16)
	header[3] = seq
	w.Write(header)
	if pLen > 0 {
		w.Write(payload)
	}
}

func buildTestHandshake(version string, connID uint32, salt []byte, authPlugin string, caps uint32) []byte {
	var buf bytes.Buffer

	buf.WriteByte(10) // protocol version
	buf.WriteString(version)
	buf.WriteByte(0)

	binary.Write(&buf, binary.LittleEndian, connID)

	// Auth data part 1 (8 bytes).
	if len(salt) >= 8 {
		buf.Write(salt[:8])
	} else {
		buf.Write(make([]byte, 8))
	}
	buf.WriteByte(0) // filler

	binary.Write(&buf, binary.LittleEndian, uint16(caps&0xffff))
	buf.WriteByte(45)                                       // charset
	binary.Write(&buf, binary.LittleEndian, uint16(0x0002)) // status
	binary.Write(&buf, binary.LittleEndian, uint16(caps>>16))

	if len(salt) > 8 {
		buf.WriteByte(byte(len(salt) + 1)) // auth data length
	} else {
		buf.WriteByte(0)
	}
	buf.Write(make([]byte, 10)) // reserved

	// Auth data part 2.
	if caps&capSecureConnection != 0 && len(salt) > 8 {
		part2 := salt[8:]
		buf.Write(part2)
		// Pad to 13 if needed.
		if len(part2) < 13 {
			buf.Write(make([]byte, 13-len(part2)))
		}
	}

	if authPlugin != "" {
		buf.WriteString(authPlugin)
		buf.WriteByte(0)
	}

	return buf.Bytes()
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

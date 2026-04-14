package mssqlproxy

import (
	"bytes"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
)

func TestSQLBatchQueryCapture(t *testing.T) {
	svc := &Service{
		cfg:        Config{QueryMaxBytes: 4096},
		logger:     discardLogger(),
		queryLogCh: make(chan queryLogEvent, 8),
	}

	query := "SELECT 42"
	batch := append(make([]byte, 4), encodeUTF16LE(query)...)
	binary.LittleEndian.PutUint32(batch[:4], 4)

	var client bytes.Buffer
	writeTDSMessageForTest(&client, tdsPacketSQLBatch, batch)

	reg := SessionRegistration{SessionID: "s1", UserID: "u1", AssetID: "a1", Engine: "mssql"}
	if err := svc.forwardClientMessages(reg, &client, io.Discard, &connectionPreparedState{}); err != nil {
		t.Fatalf("forwardClientMessages: %v", err)
	}

	close(svc.queryLogCh)
	events := collectEvents(svc.queryLogCh)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Query != query {
		t.Fatalf("query = %q, want %q", events[0].Query, query)
	}
	if events[0].ProtocolType != "sql_batch" {
		t.Fatalf("protocol_type = %q, want sql_batch", events[0].ProtocolType)
	}
	if events[0].Prepared {
		t.Fatalf("prepared should be false for SQL batch")
	}
}

func TestRPCPreparedFlowCapture(t *testing.T) {
	svc := &Service{
		cfg:        Config{QueryMaxBytes: 4096},
		logger:     discardLogger(),
		queryLogCh: make(chan queryLogEvent, 8),
	}

	prepareSQL := "SELECT * FROM users WHERE id=@P0"

	var client bytes.Buffer
	writeTDSMessageForTest(&client, tdsPacketRPC, buildRPCPayload("sp_prepexec", prepareSQL))
	writeTDSMessageForTest(&client, tdsPacketRPC, buildRPCPayload("sp_execute"))

	reg := SessionRegistration{SessionID: "s2", UserID: "u1", AssetID: "a1", Engine: "sqlserver"}
	if err := svc.forwardClientMessages(reg, &client, io.Discard, &connectionPreparedState{}); err != nil {
		t.Fatalf("forwardClientMessages: %v", err)
	}

	close(svc.queryLogCh)
	events := collectEvents(svc.queryLogCh)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	for i := range events {
		if events[i].Query != prepareSQL {
			t.Fatalf("event %d query = %q, want %q", i, events[i].Query, prepareSQL)
		}
		if events[i].ProtocolType != "rpc" {
			t.Fatalf("event %d protocol_type = %q, want rpc", i, events[i].ProtocolType)
		}
		if !events[i].Prepared {
			t.Fatalf("event %d expected prepared=true", i)
		}
	}
}

func TestRegisterSessionValidation(t *testing.T) {
	svc := &Service{
		cfg: Config{
			BindHost:   "127.0.0.1",
			PublicHost: "127.0.0.1",
		},
		logger:    discardLogger(),
		listeners: map[string]net.Listener{},
	}

	_, _, err := svc.RegisterSession(SessionRegistration{})
	if err == nil {
		t.Fatalf("expected error for missing session/user/asset")
	}
	if !strings.Contains(err.Error(), "session_id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRewriteLogin7WithManagedCreds(t *testing.T) {
	orig := buildLogin7ForTest("client_user", "client_pw", "db1")
	out, err := rewriteLogin7WithManagedCreds(orig, "pam_user", "pam_secret", "db2")
	if err != nil {
		t.Fatalf("rewriteLogin7WithManagedCreds: %v", err)
	}

	fields := parseLogin7Fields(out)
	if fields == nil {
		t.Fatalf("expected valid login7 fields")
	}
	user := decodeUTF16LE(fields[1].raw)
	if user != "pam_user" {
		t.Fatalf("username = %q, want pam_user", user)
	}
	db := parseLogin7Database(out)
	if db != "db2" {
		t.Fatalf("database = %q, want db2", db)
	}
	if len(fields[9].raw) != 0 || fields[9].chars != 0 {
		t.Fatalf("expected SSPI to be cleared for SQL auth")
	}
}

func TestPreloginResponsePacketTypeCompatibility(t *testing.T) {
	var buf bytes.Buffer
	payload := buildPreloginMessage(tdsEncryptOff)
	if err := writeTDSPayload(&buf, tdsPacketResponse, payload); err != nil {
		t.Fatalf("writeTDSPayload: %v", err)
	}
	msg, err := readTDSMessage(&buf)
	if err != nil {
		t.Fatalf("readTDSMessage: %v", err)
	}
	if msg.PacketType != tdsPacketResponse {
		t.Fatalf("packet type = 0x%02x, want 0x%02x", msg.PacketType, tdsPacketResponse)
	}
	enc, ok := parsePreloginEncryption(msg.Payload)
	if !ok {
		t.Fatalf("expected encryption token in prelogin payload")
	}
	if enc != tdsEncryptOff {
		t.Fatalf("encryption = 0x%02x, want 0x%02x", enc, tdsEncryptOff)
	}
}

func collectEvents(ch <-chan queryLogEvent) []queryLogEvent {
	out := make([]queryLogEvent, 0)
	for evt := range ch {
		out = append(out, evt)
	}
	return out
}

func writeTDSMessageForTest(w io.Writer, packetType byte, payload []byte) {
	header := make([]byte, 8)
	header[0] = packetType
	header[1] = tdsStatusEOM
	binary.BigEndian.PutUint16(header[2:4], uint16(len(payload)+8))
	_, _ = w.Write(header)
	_, _ = w.Write(payload)
}

func buildRPCPayload(procName string, args ...string) []byte {
	nameU16 := encodeUTF16LE(procName)
	buf := bytes.NewBuffer(nil)
	_ = binary.Write(buf, binary.LittleEndian, uint16(len([]rune(procName))))
	_, _ = buf.Write(nameU16)
	_ = binary.Write(buf, binary.LittleEndian, uint16(0)) // option flags
	for _, arg := range args {
		_, _ = buf.Write(encodeUTF16LE(arg))
	}
	return buf.Bytes()
}

func buildLogin7ForTest(username, password, database string) []byte {
	fixed := make([]byte, login7FixedLen)
	binary.LittleEndian.PutUint32(fixed[0:4], uint32(login7FixedLen))
	binary.LittleEndian.PutUint32(fixed[4:8], 0x74000004) // tds version
	binary.LittleEndian.PutUint32(fixed[8:12], 4096)

	fields := []loginField{
		{offsetPos: 36, chars: 0, raw: nil},
		{offsetPos: 40, chars: len([]rune(username)), raw: encodeUTF16LE(username)},
		{offsetPos: 44, chars: len([]rune(password)), raw: obfuscateTDSPassword(password)},
		{offsetPos: 48, chars: 0, raw: nil},
		{offsetPos: 52, chars: 0, raw: nil},
		{offsetPos: 56, chars: 0, raw: nil},
		{offsetPos: 60, chars: 0, raw: nil},
		{offsetPos: 64, chars: 0, raw: nil},
		{offsetPos: 68, chars: len([]rune(database)), raw: encodeUTF16LE(database)},
		{offsetPos: 78, chars: 0, raw: nil, isSSPI: true},
		{offsetPos: 82, chars: 0, raw: nil},
		{offsetPos: 86, chars: 0, raw: nil},
	}
	varBuf := make([]byte, 0)
	for i := range fields {
		off := login7FixedLen + len(varBuf)
		binary.LittleEndian.PutUint16(fixed[fields[i].offsetPos:fields[i].offsetPos+2], uint16(off))
		binary.LittleEndian.PutUint16(fixed[fields[i].offsetPos+2:fields[i].offsetPos+4], uint16(fields[i].chars))
		varBuf = append(varBuf, fields[i].raw...)
	}
	out := append(fixed, varBuf...)
	binary.LittleEndian.PutUint32(out[0:4], uint32(len(out)))
	return out
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

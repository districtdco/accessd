package sshproxy

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/districtdco/accessd/api/internal/sessions"
	"golang.org/x/crypto/ssh"
)

func TestSFTPUploadWriteCapture(t *testing.T) {
	state := newSFTPRelayState()

	openReq := buildSFTPRequestPacket(sftpPacketOpen, 1, append(sftpString("/tmp/upload.txt"), make([]byte, 4)...))
	if ops := parseSFTPClientPacket(openReq, state); len(ops) != 0 {
		t.Fatalf("expected no direct op for open, got %d", len(ops))
	}

	handleResp := buildSFTPResponsePacket(sftpPacketHandle, 1, sftpString("h1"))
	_ = parseSFTPServerPacket(handleResp, state)

	body := make([]byte, 0)
	body = append(body, sftpString("h1")...)
	body = append(body, make([]byte, 8)...)
	body = append(body, sftpString("hello-world")...)
	writeReq := buildSFTPRequestPacket(sftpPacketWrite, 2, body)

	ops := parseSFTPClientPacket(writeReq, state)
	if len(ops) != 1 {
		t.Fatalf("expected 1 write op, got %d", len(ops))
	}
	if ops[0].Operation != "upload_write" {
		t.Fatalf("operation = %q, want upload_write", ops[0].Operation)
	}
	if ops[0].Path != "/tmp/upload.txt" {
		t.Fatalf("path = %q, want /tmp/upload.txt", ops[0].Path)
	}
	if ops[0].Size != int64(len("hello-world")) {
		t.Fatalf("size = %d, want %d", ops[0].Size, len("hello-world"))
	}
}

func TestSFTPDownloadReadCapture(t *testing.T) {
	state := newSFTPRelayState()

	_ = parseSFTPClientPacket(buildSFTPRequestPacket(sftpPacketOpen, 1, append(sftpString("/tmp/download.txt"), make([]byte, 4)...)), state)
	_ = parseSFTPServerPacket(buildSFTPResponsePacket(sftpPacketHandle, 1, sftpString("h2")), state)

	readBody := make([]byte, 0)
	readBody = append(readBody, sftpString("h2")...)
	readBody = append(readBody, make([]byte, 8)...)
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, 4096)
	readBody = append(readBody, lenBuf...)
	_ = parseSFTPClientPacket(buildSFTPRequestPacket(sftpPacketRead, 9, readBody), state)

	dataResp := buildSFTPResponsePacket(sftpPacketData, 9, sftpString("payload-bytes"))
	ops := parseSFTPServerPacket(dataResp, state)
	if len(ops) != 1 {
		t.Fatalf("expected 1 read op, got %d", len(ops))
	}
	if ops[0].Operation != "download_read" {
		t.Fatalf("operation = %q, want download_read", ops[0].Operation)
	}
	if ops[0].Path != "/tmp/download.txt" {
		t.Fatalf("path = %q, want /tmp/download.txt", ops[0].Path)
	}
	if ops[0].Size != int64(len("payload-bytes")) {
		t.Fatalf("size = %d, want %d", ops[0].Size, len("payload-bytes"))
	}
}

func TestSFTPDeleteAndRenameCapture(t *testing.T) {
	state := newSFTPRelayState()

	removeReq := buildSFTPRequestPacket(sftpPacketRemove, 11, sftpString("/tmp/old.txt"))
	ops := parseSFTPClientPacket(removeReq, state)
	if len(ops) != 1 || ops[0].Operation != "delete" || ops[0].Path != "/tmp/old.txt" {
		t.Fatalf("unexpected remove op: %#v", ops)
	}

	renameBody := append(sftpString("/tmp/a.txt"), sftpString("/tmp/b.txt")...)
	renameReq := buildSFTPRequestPacket(sftpPacketRename, 12, renameBody)
	ops = parseSFTPClientPacket(renameReq, state)
	if len(ops) != 1 {
		t.Fatalf("expected 1 rename op, got %d", len(ops))
	}
	if ops[0].Operation != "rename" || ops[0].Path != "/tmp/a.txt" || ops[0].PathTo != "/tmp/b.txt" {
		t.Fatalf("unexpected rename op: %#v", ops[0])
	}
}

func TestBuildFileOperationPayload(t *testing.T) {
	launch := sessions.LaunchContext{SessionID: "s1", UserID: "u1", AssetID: "a1"}
	op := sftpFileOperation{Operation: "rename", Path: "/tmp/a", PathTo: "/tmp/b", Size: 64}
	when := time.Date(2026, 4, 7, 1, 2, 3, 0, time.UTC)

	payload := buildFileOperationPayload(launch, op, when)
	if payload["session_id"] != "s1" || payload["user_id"] != "u1" || payload["asset_id"] != "a1" {
		t.Fatalf("missing identity fields: %#v", payload)
	}
	if payload["operation"] != "rename" || payload["path"] != "/tmp/a" || payload["path_to"] != "/tmp/b" {
		t.Fatalf("missing path/operation fields: %#v", payload)
	}
	if payload["size"] != int64(64) {
		t.Fatalf("size field mismatch: %#v", payload["size"])
	}
}

func TestLaunchFromPermissions_IncludesRequestID(t *testing.T) {
	launch, err := launchFromPermissions(&ssh.Permissions{
		Extensions: map[string]string{
			"session_id": "s1",
			"user_id":    "u1",
			"asset_id":   "a1",
			"request_id": "req-123",
			"host":       "127.0.0.1",
			"port":       "22",
			"protocol":   "ssh",
			"action":     "shell",
		},
	})
	if err != nil {
		t.Fatalf("launchFromPermissions returned error: %v", err)
	}
	if launch.RequestID != "req-123" {
		t.Fatalf("request id = %q, want req-123", launch.RequestID)
	}
}

func buildSFTPRequestPacket(packetType byte, requestID uint32, body []byte) []byte {
	payload := make([]byte, 0, 1+4+len(body))
	payload = append(payload, packetType)
	id := make([]byte, 4)
	binary.BigEndian.PutUint32(id, requestID)
	payload = append(payload, id...)
	payload = append(payload, body...)
	return payload
}

func buildSFTPResponsePacket(packetType byte, requestID uint32, body []byte) []byte {
	return buildSFTPRequestPacket(packetType, requestID, body)
}

func sftpString(v string) []byte {
	buf := make([]byte, 4+len(v))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(v)))
	copy(buf[4:], []byte(v))
	return buf
}

package launch

import (
	"strings"
	"testing"
)

func TestSanitizeCommandArg_DBeaverPassword(t *testing.T) {
	arg := "driver=sqlserver|host=db.local|user=sa|password=supersecret|database=master"
	got := sanitizeCommandArg(arg)
	if strings.Contains(got, "supersecret") {
		t.Fatalf("expected password to be redacted, got %q", got)
	}
	if !strings.Contains(got, "password=<redacted>") {
		t.Fatalf("expected redacted password marker, got %q", got)
	}
}

func TestSanitizeCommandArg_SFTPURLPassword(t *testing.T) {
	arg := "sftp://alice:ultrasecret@example.com:22/home/alice"
	got := sanitizeCommandArg(arg)
	if strings.Contains(got, "ultrasecret") {
		t.Fatalf("expected sftp password to be redacted, got %q", got)
	}
	if !strings.Contains(got, "alice:<redacted>@") {
		t.Fatalf("expected redacted sftp password marker, got %q", got)
	}
}

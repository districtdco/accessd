package connutil

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestWrapIdleTimeout_ReadTimesOutWhenIdle(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	wrapped := WrapIdleTimeout(c1, 20*time.Millisecond)
	buf := make([]byte, 1)
	start := time.Now()
	_, err := wrapped.Read(buf)
	if err == nil {
		t.Fatalf("expected timeout error")
	}

	var nerr net.Error
	if !errors.As(err, &nerr) || !nerr.Timeout() {
		t.Fatalf("expected timeout net.Error, got %T %v", err, err)
	}
	if time.Since(start) < 10*time.Millisecond {
		t.Fatalf("read returned too quickly to have applied deadline")
	}
}

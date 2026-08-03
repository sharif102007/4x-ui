package service

import (
	"errors"
	"net"
	"testing"
)

type noDelayRecorder struct {
	net.Conn
	called  bool
	enabled bool
	err     error
}

func (c *noDelayRecorder) SetNoDelay(enabled bool) error {
	c.called = true
	c.enabled = enabled
	return c.err
}

func TestEnableTCPNoDelayEnablesSupportedConnection(t *testing.T) {
	conn := &noDelayRecorder{}
	if err := enableTCPNoDelay(conn); err != nil {
		t.Fatal(err)
	}
	if !conn.called || !conn.enabled {
		t.Fatalf("SetNoDelay call = (%v, %v), want (true, true)", conn.called, conn.enabled)
	}
}

func TestEnableTCPNoDelayReturnsSocketError(t *testing.T) {
	want := errors.New("setsockopt failed")
	conn := &noDelayRecorder{err: want}
	if err := enableTCPNoDelay(conn); !errors.Is(err, want) {
		t.Fatalf("enableTCPNoDelay error = %v, want %v", err, want)
	}
}

func TestEnableTCPNoDelayAllowsNonTCPConnection(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if err := enableTCPNoDelay(a); err != nil {
		t.Fatalf("non-TCP connection should pass through: %v", err)
	}
}

func TestEnableTCPNoDelayRejectsNilConnection(t *testing.T) {
	if err := enableTCPNoDelay(nil); err == nil {
		t.Fatal("nil connection should return an error")
	}
}

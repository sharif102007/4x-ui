package service

import (
	"net"
	"testing"
)

func TestUdpRelayReadyDetectsListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if !udpRelayReady(port) {
		_ = listener.Close()
		t.Fatal("live loopback listener was not detected")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if udpRelayReady(port) {
		t.Fatal("closed loopback listener was reported as ready")
	}
}

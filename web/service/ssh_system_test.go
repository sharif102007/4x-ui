package service

import (
	"strings"
	"testing"
)

func TestBuildStunnelConfigEnablesTCPNoDelayOnBothSides(t *testing.T) {
	config := buildStunnelConfig([]stunnelSvc{{
		Name:        "ssh_tls_payload_1",
		AcceptPort:  443,
		ConnectPort: 8880,
		CertFile:    "/tmp/fullchain.pem",
		KeyFile:     "/tmp/privkey.pem",
	}})

	for _, want := range []string{
		"socket = l:TCP_NODELAY=1",
		"socket = r:TCP_NODELAY=1",
		"[ssh_tls_payload_1]",
		"accept = 0.0.0.0:443",
		"connect = 127.0.0.1:8880",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("stunnel config missing %q:\n%s", want, config)
		}
	}
	if got := strings.Count(config, "TCP_NODELAY=1"); got != 2 {
		t.Fatalf("TCP_NODELAY entries = %d, want 2", got)
	}
}

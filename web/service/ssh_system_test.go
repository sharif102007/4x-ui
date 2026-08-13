package service

import "testing"

func TestSshdTestConnectionSuppliesLocalPort(t *testing.T) {
	if sshdTestConnection != "user=root,host=localhost,addr=127.0.0.1,laddr=127.0.0.1,lport=22" {
		t.Fatalf("sshd test connection must supply every Match criterion, got %q", sshdTestConnection)
	}
}

func TestSshdPortsFromText(t *testing.T) {
	ports := sshdPortsFromText("port 22\nPort 2222\nport nope\nport 70000\nbanner /tmp/banner\n")
	for _, port := range []int{22, 2222} {
		if _, ok := ports[port]; !ok {
			t.Fatalf("expected port %d to be parsed from sshd output", port)
		}
	}
	if len(ports) != 2 {
		t.Fatalf("unexpected parsed ports: %#v", ports)
	}
}

func TestCertificateIdentity(t *testing.T) {
	tests := []struct {
		host string
		cn   string
		san  string
	}{
		{"example.com", "example.com", "DNS:example.com"},
		{"203.0.113.7", "203.0.113.7", "IP:203.0.113.7"},
		{"2001:db8::1", "2001:db8::1", "IP:2001:db8::1"},
		{"bad/name", "ssh.local", "DNS:ssh.local"},
		{"", "ssh.local", "DNS:ssh.local"},
	}
	for _, tc := range tests {
		cn, san := certificateIdentity(tc.host)
		if cn != tc.cn || san != tc.san {
			t.Fatalf("certificateIdentity(%q) = %q, %q; want %q, %q", tc.host, cn, san, tc.cn, tc.san)
		}
	}
}

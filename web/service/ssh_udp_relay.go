package service

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/sharif102007/4x-ui/v2/logger"
)

// udpgwMTU is the largest UDP payload the relay will forward in one piece.
// Anything larger is dropped by udpgw rather than fragmented, so this has to
// sit above the tun MTU of the client app; Android tunnelling clients use 1500
// by default, so 8192 leaves a wide margin while keeping per-connection buffers
// small enough to matter on a 1 GB VPS.
//
// Override with XUI_UDPGW_MTU=<bytes> in /etc/default/x-ui if a client
// negotiates something larger. Set it to 0 to omit the flag entirely and fall
// back to whatever the installed badvpn build defaults to.
const defaultUdpgwMTU = 8192

func udpgwMTU() int {
	raw := os.Getenv("XUI_UDPGW_MTU")
	if raw == "" {
		return defaultUdpgwMTU
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		logger.Warningf("ssh-manager: ignoring invalid XUI_UDPGW_MTU=%q", raw)
		return defaultUdpgwMTU
	}
	return v
}

// udpRelayProc supervises a single badvpn-udpgw process for one inbound.
// badvpn-udpgw listens on 127.0.0.1:port (only reachable via the SSH tunnel)
// and relays UDP datagrams, allowing clients to send/receive UDP through TCP SSH.
type udpRelayProc struct {
	id     int
	port   int
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

var (
	udpRelayMu sync.Mutex
	udpRelays  = map[int]*udpRelayProc{} // key: inbound ID
)

// BadvpnInstalled reports whether badvpn-udpgw is available.
func BadvpnInstalled() bool {
	_, err := exec.LookPath("badvpn-udpgw")
	if err == nil {
		return true
	}
	// also check common absolute paths
	for _, p := range []string{"/usr/bin/badvpn-udpgw", "/usr/local/bin/badvpn-udpgw"} {
		if _, err2 := exec.LookPath(p); err2 == nil {
			return true
		}
	}
	return false
}

// EnsureBadvpn tries to install badvpn via apt-get; if not in repos (Debian 12),
// falls back to compiling udpgw from the official GitHub source.
func EnsureBadvpn() error {
	if BadvpnInstalled() {
		return nil
	}
	logger.Info("ssh-manager: installing badvpn (apt)...")
	var s sshSystem

	// Try apt first (works on Ubuntu, older Debian)
	out, err := s.runWithTimeout(5*time.Minute, "apt-get", "install", "-y", "badvpn")
	if err == nil && BadvpnInstalled() {
		logger.Info("ssh-manager: badvpn installed via apt")
		return nil
	}
	logger.Warningf("ssh-manager: apt badvpn not available (%s), compiling from source...", out)

	// Compile from source (required on Debian 12 — not in repos)
	deps := []string{"build-essential", "cmake", "git"}
	if _, err2 := s.runWithTimeout(5*time.Minute, "apt-get", append([]string{"install", "-y"}, deps...)...); err2 != nil {
		return fmt.Errorf("build deps install failed: %v", err2)
	}
	_, _ = s.run("rm", "-rf", "/tmp/badvpn-src")
	steps := [][]string{
		{"git", "clone", "--depth=1", "https://github.com/ambrop72/badvpn.git", "/tmp/badvpn-src"},
		{"cmake", "-S", "/tmp/badvpn-src", "-B", "/tmp/badvpn-src/build",
			"-DBUILD_NOTHING_BY_DEFAULT=1", "-DBUILD_UDPGW=1"},
		{"make", "-C", "/tmp/badvpn-src/build", "-j4"},
	}
	for _, args := range steps {
		if o, e := s.runWithTimeout(5*time.Minute, args[0], args[1:]...); e != nil {
			return fmt.Errorf("badvpn compile failed (%v): %s", args[0], o)
		}
	}
	if o, e := s.run("cp", "/tmp/badvpn-src/build/udpgw/badvpn-udpgw", "/usr/local/bin/badvpn-udpgw"); e != nil {
		return fmt.Errorf("badvpn copy failed: %s", o)
	}
	_, _ = s.run("chmod", "+x", "/usr/local/bin/badvpn-udpgw")
	// clean up
	_, _ = s.run("rm", "-rf", "/tmp/badvpn-src")

	if !BadvpnInstalled() {
		return fmt.Errorf("badvpn-udpgw not found after compile; install manually")
	}
	logger.Info("ssh-manager: badvpn compiled and installed from source")
	return nil
}

// startUdpRelay starts a supervised badvpn-udpgw for the given inbound.
// Runs as a goroutine, restarts on crash up to 10 times with backoff. The
// caller only gets success after the TCP udpgw endpoint is actually accepting
// connections, so an unsupported flag or occupied port cannot be reported as
// a healthy inbound.
func startUdpRelay(id, port int) error {
	ctx, cancel := context.WithCancel(context.Background())
	proc := &udpRelayProc{id: id, port: port, ctx: ctx, cancel: cancel, done: make(chan struct{})}

	udpRelayMu.Lock()
	udpRelays[id] = proc
	udpRelayMu.Unlock()

	go proc.supervise()
	if waitUdpRelayReady(proc, 5*time.Second) {
		return nil
	}
	proc.cancel()
	waitChanWithTimeout(proc.done, "udp relay (failed startup)")
	return fmt.Errorf("badvpn-udpgw did not listen on 127.0.0.1:%d", port)
}

func udpRelayReady(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 150*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func waitUdpRelayReady(proc *udpRelayProc, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(75 * time.Millisecond)
	defer ticker.Stop()
	for {
		if udpRelayReady(proc.port) {
			return true
		}
		select {
		case <-proc.done:
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

func (p *udpRelayProc) supervise() {
	defer func() {
		udpRelayMu.Lock()
		if udpRelays[p.id] == p {
			delete(udpRelays, p.id)
		}
		udpRelayMu.Unlock()
		close(p.done)
	}()
	failures := 0
	for {
		select {
		case <-p.ctx.Done():
			return
		default:
		}
		if err := p.run(); err != nil {
			failures++
			logger.Warningf("ssh-manager: udpgw inbound#%d crashed (attempt %d): %v", p.id, failures, err)
			if failures >= 10 {
				logger.Errorf("ssh-manager: udpgw inbound#%d giving up after %d failures", p.id, failures)
				return
			}
			delay := time.Duration(failures) * 2 * time.Second
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			select {
			case <-p.ctx.Done():
				return
			case <-time.After(delay):
			}
		} else {
			// clean exit — context was cancelled
			return
		}
	}
}

func (p *udpRelayProc) run() error {
	udpgwBin, err := exec.LookPath("badvpn-udpgw")
	if err != nil {
		udpgwBin = "/usr/bin/badvpn-udpgw"
	}
	addr := fmt.Sprintf("127.0.0.1:%d", p.port)
	args := []string{
		"--listen-addr", addr,
		"--max-clients", "500",
		"--max-connections-for-client", "10",
	}
	if mtu := udpgwMTU(); mtu > 0 {
		args = append(args, "--udp-mtu", strconv.Itoa(mtu))
	}
	cmd := exec.CommandContext(p.ctx, udpgwBin, args...)
	logger.Infof("ssh-manager: udpgw inbound#%d up %s", p.id, addr)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if p.ctx.Err() != nil {
		return nil // intentional shutdown
	}
	if err != nil && stderr.Len() > 0 {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}
	return err
}

// reconcileUdpRelays starts/stops badvpn-udpgw processes to match desired.
// desired: map[inboundID]port  (0 = disabled)
func reconcileUdpRelays(desired map[int]int) error {
	udpRelayMu.Lock()
	toStop := make([]*udpRelayProc, 0)
	// Stop relays not in desired or with changed port.
	for id, proc := range udpRelays {
		wantPort, ok := desired[id]
		if !ok || wantPort != proc.port || !udpRelayReady(proc.port) {
			delete(udpRelays, id)
			toStop = append(toStop, proc)
		}
	}
	toStart := map[int]int{}
	for id, port := range desired {
		if _, running := udpRelays[id]; !running {
			toStart[id] = port
		}
	}
	udpRelayMu.Unlock()

	for _, proc := range toStop {
		proc.cancel()
		waitChanWithTimeout(proc.done, "udp relay (reconcile)")
	}
	for id, port := range toStart {
		if err := startUdpRelay(id, port); err != nil {
			return fmt.Errorf("start UDP relay for inbound %d: %w", id, err)
		}
	}
	return nil
}

// StopAllUdpRelays shuts down every running badvpn-udpgw (called on panel shutdown).
func StopAllUdpRelays() {
	udpRelayMu.Lock()
	procs := make([]*udpRelayProc, 0, len(udpRelays))
	for _, p := range udpRelays {
		procs = append(procs, p)
	}
	udpRelays = map[int]*udpRelayProc{}
	udpRelayMu.Unlock()

	for _, p := range procs {
		p.cancel()
		waitChanWithTimeout(p.done, "udp relay")
	}
}

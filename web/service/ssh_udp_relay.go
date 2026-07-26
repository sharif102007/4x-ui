package service

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/sharif102007/4x-ui/v2/logger"
)

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
	out, err := s.run("apt-get", "install", "-y", "badvpn")
	if err == nil && BadvpnInstalled() {
		logger.Info("ssh-manager: badvpn installed via apt")
		return nil
	}
	logger.Warningf("ssh-manager: apt badvpn not available (%s), compiling from source...", out)

	// Compile from source (required on Debian 12 — not in repos)
	deps := []string{"build-essential", "cmake", "git"}
	if _, err2 := s.run("apt-get", append([]string{"install", "-y"}, deps...)...); err2 != nil {
		return fmt.Errorf("build deps install failed: %v", err2)
	}
	steps := [][]string{
		{"git", "clone", "--depth=1", "https://github.com/ambrop72/badvpn.git", "/tmp/badvpn-src"},
		{"cmake", "-S", "/tmp/badvpn-src", "-B", "/tmp/badvpn-src/build",
			"-DBUILD_NOTHING_BY_DEFAULT=1", "-DBUILD_UDPGW=1"},
		{"make", "-C", "/tmp/badvpn-src/build", "-j4"},
	}
	for _, args := range steps {
		if o, e := s.run(args[0], args[1:]...); e != nil {
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
// Runs as a goroutine, restarts on crash up to 5 times with backoff.
func startUdpRelay(id, port int) {
	ctx, cancel := context.WithCancel(context.Background())
	proc := &udpRelayProc{id: id, port: port, ctx: ctx, cancel: cancel, done: make(chan struct{})}

	udpRelayMu.Lock()
	udpRelays[id] = proc
	udpRelayMu.Unlock()

	go proc.supervise()
}

func (p *udpRelayProc) supervise() {
	defer close(p.done)
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
	cmd := exec.CommandContext(p.ctx, udpgwBin,
		"--listen-addr", addr,
		"--max-clients", "500",
		"--max-connections-for-client", "10",
	)
	logger.Infof("ssh-manager: udpgw inbound#%d up %s", p.id, addr)
	err = cmd.Run()
	if p.ctx.Err() != nil {
		return nil // intentional shutdown
	}
	return err
}

// reconcileUdpRelays starts/stops badvpn-udpgw processes to match desired.
// desired: map[inboundID]port  (0 = disabled)
func reconcileUdpRelays(desired map[int]int) {
	udpRelayMu.Lock()
	// Stop relays not in desired or with changed port.
	for id, proc := range udpRelays {
		wantPort, ok := desired[id]
		if !ok || wantPort != proc.port {
			delete(udpRelays, id)
			go func(p *udpRelayProc) {
				p.cancel()
				<-p.done
			}(proc)
		}
	}
	toStart := map[int]int{}
	for id, port := range desired {
		if _, running := udpRelays[id]; !running {
			toStart[id] = port
		}
	}
	udpRelayMu.Unlock()

	for id, port := range toStart {
		startUdpRelay(id, port)
	}
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
		<-p.done
	}
}

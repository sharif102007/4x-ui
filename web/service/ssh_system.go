package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sharif102007/4x-ui/v2/logger"
)

// Filesystem locations owned by the SSH Manager. These are fixed, never built
// from user input, so there is no path-traversal surface here.
const (
	sshdDropinPath  = "/etc/ssh/sshd_config.d/99-xui-ssh-manager.conf"
	stunnelConfPath = "/etc/stunnel/xui-ssh-manager.conf"
	stunnelUnitPath = "/etc/systemd/system/xui-stunnel.service"
	bannerDir       = "/etc/x-ui/ssh-manager/banners"
	certDir         = "/etc/x-ui/ssh-manager/certs"

	sshUsersGroup = "xui-ssh-users"

	cmdTimeout = 30 * time.Second
)

// usernameRe enforces the spec: A-Z a-z 0-9 underscore hyphen, first char not a
// hyphen (useradd rejects leading '-'), max 32 chars.
var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,31}$`)

// protectedUsers must never be created, modified, disabled or deleted by the
// panel. Includes the names called out in the spec plus the standard Debian
// system accounts.
var protectedUsers = map[string]struct{}{
	"root": {}, "ubuntu": {}, "debian": {}, "www-data": {}, "x-ui": {},
	"nobody": {}, "daemon": {}, "bin": {}, "sys": {}, "sync": {}, "games": {},
	"man": {}, "lp": {}, "mail": {}, "news": {}, "uucp": {}, "proxy": {},
	"backup": {}, "list": {}, "irc": {}, "_apt": {}, "sshd": {}, "messagebus": {},
	"systemd-network": {}, "systemd-resolve": {}, "systemd-timesync": {},
	"polkitd": {}, "tss": {}, "tcpdump": {}, "landscape": {},
}

// sshSystem is the stateless low-level executor. All methods shell out through
// run/runStdin which use exec.Command with an explicit argv (never a shell
// string), so user-supplied values cannot inject commands.
type sshSystem struct{}

// run executes a command with an explicit argument vector and a timeout.
func (sshSystem) run(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// runStdin executes a command feeding stdin from a string (used for chpasswd so
// the password never appears in argv or in the process table).
func (sshSystem) runStdin(stdin string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// tool resolves a binary, trying PATH first then a list of common absolute
// fallbacks (useradd/sshd live in /usr/sbin which is not always in PATH for
// the service environment).
func (sshSystem) tool(name string, fallbacks ...string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, f := range fallbacks {
		if _, err := os.Stat(f); err == nil {
			return f, nil
		}
	}
	return "", fmt.Errorf("required tool %q not found on this system", name)
}

// ---------------------------------------------------------------------------
// Validation helpers
// ---------------------------------------------------------------------------

func validUsername(u string) error {
	if !usernameRe.MatchString(u) {
		return errors.New("invalid username: use only A-Z a-z 0-9 _ - (max 32, no leading hyphen)")
	}
	return nil
}

func isProtectedUser(u string) bool {
	_, ok := protectedUsers[strings.ToLower(u)]
	return ok
}

// validPassword rejects only the characters that would break chpasswd stdin
// framing or leak into logs; everything else (including symbols) is allowed.
func validPassword(p string) error {
	if p == "" {
		return errors.New("password cannot be empty")
	}
	if strings.ContainsAny(p, "\n\r\x00") {
		return errors.New("password cannot contain newlines or null bytes")
	}
	return nil
}

func validPort(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("port %d out of range 1-65535", p)
	}
	return nil
}

// cleanCertPath ensures a user-provided certificate path is absolute and
// canonical (defends against traversal / relative tricks) and exists.
func cleanCertPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("empty path")
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("certificate path must be absolute")
	}
	clean := filepath.Clean(p)
	if clean != p && clean+"/" != p {
		// reject anything that normalised differently (e.g. embedded ..)
		if strings.Contains(p, "..") {
			return "", errors.New("certificate path must not contain '..'")
		}
	}
	if _, err := os.Stat(clean); err != nil {
		return "", fmt.Errorf("certificate path not accessible: %v", err)
	}
	return clean, nil
}

// ---------------------------------------------------------------------------
// Port checks
// ---------------------------------------------------------------------------

// portFree reports whether a TCP port can be bound right now on all interfaces
// and loopback. A port already held by our own sshd/stunnel will report not
// free; callers therefore only probe ports that are *new* to our config.
func (sshSystem) portFree(port int) bool {
	for _, addr := range []string{fmt.Sprintf("0.0.0.0:%d", port), fmt.Sprintf("127.0.0.1:%d", port)} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return false
		}
		_ = ln.Close()
	}
	return true
}

// freeLocalPort asks the kernel for an unused loopback port (used to
// auto-assign payload-gateway ports).
func (sshSystem) freeLocalPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// ---------------------------------------------------------------------------
// OpenSSH: drop-in management with validate + rollback. Never locks SSH out.
// ---------------------------------------------------------------------------

// applySshdPorts writes the managed drop-in so OpenSSH listens on every port in
// `ports` (additive Port directives only — existing ports such as the admin's
// current SSH port keep working because Port is cumulative and we add no
// ListenAddress lines). Optional per-port banners are emitted as trailing
// `Match LocalPort` blocks so a banner only affects that managed port and never
// the admin's main SSH port (and never via ForceCommand). Validates with
// `sshd -t` and rolls back on failure.
func (s sshSystem) applySshdPorts(ports []int, banners map[int]string) error {
	sshdBin, err := s.tool("sshd", "/usr/sbin/sshd", "/sbin/sshd")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(sshdDropinPath), 0o755); err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString("# Managed by 4x-ui SSH Manager. Do not edit by hand.\n")
	sb.WriteString("# Additive Port directives only; your existing SSH port is preserved.\n")
	seen := map[int]struct{}{}
	for _, p := range ports {
		if p <= 0 {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		sb.WriteString(fmt.Sprintf("Port %d\n", p))
	}
	// Keep tunnelling working for managed accounts without ForceCommand.
	sb.WriteString("AllowTcpForwarding yes\n")
	sb.WriteString("PermitTTY yes\n")
	sb.WriteString("PermitTunnel yes\n") // enables ssh -w TUN/TAP mode for full UDP support

	// Put modern algorithms first, but retain the older proposals required by
	// Android clients such as HTTP Custom. Some releases of those clients use
	// older SSH libraries and otherwise fail immediately with
	// "Cannot negotiate, proposals do not match". DarkTunnel supports the
	// modern defaults, which is why the same account can work there while
	// failing in HTTP Custom.
	//
	// The legacy algorithms are fallbacks only: a modern client still selects
	// the first mutually supported modern proposal. sshd -t below validates the
	// complete list before the service is restarted.
	sb.WriteString("KexAlgorithms curve25519-sha256,curve25519-sha256@libssh.org,sntrup761x25519-sha512@openssh.com,diffie-hellman-group16-sha512,diffie-hellman-group18-sha512,diffie-hellman-group14-sha256,diffie-hellman-group14-sha1,diffie-hellman-group-exchange-sha1,diffie-hellman-group1-sha1\n")
	sb.WriteString("HostKeyAlgorithms ssh-ed25519,ecdsa-sha2-nistp256,rsa-sha2-512,rsa-sha2-256,ssh-rsa\n")
	sb.WriteString("Ciphers chacha20-poly1305@openssh.com,aes128-gcm@openssh.com,aes256-gcm@openssh.com,aes128-ctr,aes192-ctr,aes256-ctr,aes128-cbc,aes192-cbc,aes256-cbc\n")
	sb.WriteString("MACs hmac-sha2-256-etm@openssh.com,hmac-sha2-512-etm@openssh.com,umac-128-etm@openssh.com,hmac-sha2-256,hmac-sha2-512,hmac-sha1\n")
	sb.WriteString("Compression no\n")

	// Optional per-port banners as trailing Match blocks (Match must come after
	// all global directives). A banner is sent pre-auth and does not interfere
	// with the SSH tunnel.
	bports := make([]int, 0, len(banners))
	for p := range banners {
		bports = append(bports, p)
	}
	sort.Ints(bports)
	for _, p := range bports {
		path := banners[p]
		if path == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("\nMatch LocalPort %d\n", p))
		sb.WriteString(fmt.Sprintf("    Banner %s\n", path))
	}

	// Snapshot previous content for rollback.
	prev, hadPrev := os.ReadFile(sshdDropinPath)

	if err := os.WriteFile(sshdDropinPath, []byte(sb.String()), 0o644); err != nil {
		return err
	}

	// Validate the *whole* effective config (sshd -t reads the main file which
	// Includes our drop-in on Debian 12).
	if out, terr := s.run(sshdBin, "-t"); terr != nil {
		// rollback
		if hadPrev == nil {
			_ = os.WriteFile(sshdDropinPath, prev, 0o644)
		} else {
			_ = os.Remove(sshdDropinPath)
		}
		return fmt.Errorf("sshd config validation failed, rolled back: %s", strings.TrimSpace(out))
	}

	if err := s.ensureSshSocketDisabled(); err != nil {
		logger.Warning("ssh-manager: could not disable ssh.socket:", err)
	}

	if err := s.restartSsh(); err != nil {
		// rollback config and try to restore the daemon to its prior state.
		if hadPrev == nil {
			_ = os.WriteFile(sshdDropinPath, prev, 0o644)
		} else {
			_ = os.Remove(sshdDropinPath)
		}
		_ = s.restartSsh()
		return fmt.Errorf("failed to restart ssh, rolled back: %v", err)
	}
	return nil
}

// ensureSshSocketDisabled stops + disables socket-activated SSH when present,
// because socket activation ignores the Port directives we manage.
func (s sshSystem) ensureSshSocketDisabled() error {
	systemctl, err := s.tool("systemctl", "/usr/bin/systemctl", "/bin/systemctl")
	if err != nil {
		return err
	}
	// Is the socket active or enabled?
	active, _ := s.run(systemctl, "is-active", "ssh.socket")
	enabled, _ := s.run(systemctl, "is-enabled", "ssh.socket")
	if strings.TrimSpace(active) == "active" || strings.TrimSpace(enabled) == "enabled" {
		logger.Info("ssh-manager: disabling conflicting ssh.socket")
		_, _ = s.run(systemctl, "stop", "ssh.socket")
		_, _ = s.run(systemctl, "disable", "ssh.socket")
	}
	return nil
}

// restartSsh restarts the OpenSSH service, trying the Debian unit name first.
func (s sshSystem) restartSsh() error {
	systemctl, err := s.tool("systemctl", "/usr/bin/systemctl", "/bin/systemctl")
	if err != nil {
		return err
	}
	_, _ = s.run(systemctl, "enable", "ssh.service")
	if out, err := s.run(systemctl, "restart", "ssh.service"); err != nil {
		// fall back to "sshd.service" on non-Debian layouts
		if out2, err2 := s.run(systemctl, "restart", "sshd.service"); err2 != nil {
			return fmt.Errorf("%s / %s", strings.TrimSpace(out), strings.TrimSpace(out2))
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Firewall: only ever *adds allow* rules, never enables a firewall or adds
// deny rules (which could drop the admin's SSH session).
// ---------------------------------------------------------------------------

func (s sshSystem) allowPort(port int) {
	if err := validPort(port); err != nil {
		return
	}
	// ufw
	if ufw, err := s.tool("ufw"); err == nil {
		status, _ := s.run(ufw, "status")
		if strings.Contains(strings.ToLower(status), "status: active") {
			if _, err := s.run(ufw, "allow", fmt.Sprintf("%d/tcp", port)); err == nil {
				logger.Infof("ssh-manager: ufw allow %d/tcp", port)
			}
			return
		}
	}
	// firewalld
	if fw, err := s.tool("firewall-cmd"); err == nil {
		state, _ := s.run(fw, "--state")
		if strings.Contains(strings.ToLower(state), "running") {
			_, _ = s.run(fw, "--permanent", fmt.Sprintf("--add-port=%d/tcp", port))
			_, _ = s.run(fw, "--reload")
			logger.Infof("ssh-manager: firewalld add-port %d/tcp", port)
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Linux users (only within the xui-ssh-users group; protected users untouched)
// ---------------------------------------------------------------------------

func (s sshSystem) ensureGroup() error {
	groupadd, err := s.tool("groupadd", "/usr/sbin/groupadd")
	if err != nil {
		return err
	}
	// -f makes it idempotent (no error if it already exists).
	if out, err := s.run(groupadd, "-f", sshUsersGroup); err != nil {
		return fmt.Errorf("groupadd: %s", strings.TrimSpace(out))
	}
	return nil
}

func (s sshSystem) userExists(username string) bool {
	id, err := s.tool("id", "/usr/bin/id")
	if err != nil {
		return false
	}
	_, e := s.run(id, "-u", username)
	return e == nil
}

// inManagedGroup reports whether the OS user is a member of xui-ssh-users.
// Used as a guard so edit/disable/delete can only ever touch managed accounts.
func (s sshSystem) inManagedGroup(username string) bool {
	idc, err := s.tool("id", "/usr/bin/id")
	if err != nil {
		return false
	}
	out, err := s.run(idc, "-nG", username)
	if err != nil {
		return false
	}
	for _, g := range strings.Fields(out) {
		if g == sshUsersGroup {
			return true
		}
	}
	return false
}

func (s sshSystem) createUser(username string) error {
	useradd, err := s.tool("useradd", "/usr/sbin/useradd")
	if err != nil {
		return err
	}
	// nologin shell: keeps interactive shells off while still permitting
	// SSH port-forwarding / dynamic tunnels (ssh -N / -D), which is what the
	// HOST:PORT@USER:PASS tunnelling apps use.
	nologin := "/usr/sbin/nologin"
	if _, err := os.Stat(nologin); err != nil {
		nologin = "/sbin/nologin"
	}
	args := []string{"-m", "-G", sshUsersGroup, "-s", nologin, username}
	if out, err := s.run(useradd, args...); err != nil {
		return fmt.Errorf("useradd: %s", strings.TrimSpace(out))
	}
	return nil
}

func (s sshSystem) setPassword(username, password string) error {
	chpasswd, err := s.tool("chpasswd", "/usr/sbin/chpasswd")
	if err != nil {
		return err
	}
	// "user:password" via stdin. chpasswd splits on the first colon only, so
	// colons inside the password are preserved.
	if out, err := s.runStdin(username+":"+password+"\n", chpasswd); err != nil {
		return fmt.Errorf("chpasswd: %s", strings.TrimSpace(out))
	}
	return nil
}

func (s sshSystem) lockUser(username string) error {
	usermod, err := s.tool("usermod", "/usr/sbin/usermod")
	if err != nil {
		return err
	}
	_, _ = s.run(usermod, "-L", username) // lock password
	if chage, err := s.tool("chage", "/usr/bin/chage"); err == nil {
		_, _ = s.run(chage, "-E", "1", username) // expire (1970-01-02)
	}
	return nil
}

func (s sshSystem) unlockUser(username string, expiry string) error {
	usermod, err := s.tool("usermod", "/usr/sbin/usermod")
	if err != nil {
		return err
	}
	_, _ = s.run(usermod, "-U", username)
	if chage, err := s.tool("chage", "/usr/bin/chage"); err == nil {
		if expiry == "" {
			_, _ = s.run(chage, "-E", "-1", username) // never expire
		} else {
			_, _ = s.run(chage, "-E", expiry, username)
		}
	}
	return nil
}

func (s sshSystem) setExpiry(username string, expiry string) {
	if chage, err := s.tool("chage", "/usr/bin/chage"); err == nil {
		if expiry == "" {
			_, _ = s.run(chage, "-E", "-1", username)
		} else {
			_, _ = s.run(chage, "-E", expiry, username)
		}
	}
}

func (s sshSystem) deleteUser(username string) error {
	userdel, err := s.tool("userdel", "/usr/sbin/userdel")
	if err != nil {
		return err
	}
	if out, err := s.run(userdel, "-r", username); err != nil {
		// -r can warn about mail spool; treat "not exist" as success.
		low := strings.ToLower(out)
		if strings.Contains(low, "does not exist") {
			return nil
		}
		return fmt.Errorf("userdel: %s", strings.TrimSpace(out))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Certificates (self-signed generation)
// ---------------------------------------------------------------------------

func (s sshSystem) selfSignedCert(id int, host string) (string, string, error) {
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		return "", "", err
	}
	crt := filepath.Join(certDir, fmt.Sprintf("ssh-%d.crt", id))
	key := filepath.Join(certDir, fmt.Sprintf("ssh-%d.key", id))
	if _, err := os.Stat(crt); err == nil {
		if _, err := os.Stat(key); err == nil {
			return crt, key, nil // reuse existing
		}
	}
	openssl, err := s.tool("openssl", "/usr/bin/openssl")
	if err != nil {
		return "", "", err
	}
	cn := host
	if cn == "" {
		cn = "ssh.local"
	}
	args := []string{
		"req", "-x509", "-nodes", "-newkey", "rsa:2048",
		"-keyout", key, "-out", crt, "-days", "3650",
		"-subj", "/CN=" + cn,
		"-addext", "subjectAltName=DNS:" + cn,
	}
	if out, err := s.run(openssl, args...); err != nil {
		return "", "", fmt.Errorf("openssl: %s", strings.TrimSpace(out))
	}
	_ = os.Chmod(key, 0o600)
	return crt, key, nil
}

// ---------------------------------------------------------------------------
// stunnel: dedicated isolated instance so we never disturb any other stunnel
// the operator may run, and never collide with 4x-ui/Xray TLS ports.
// ---------------------------------------------------------------------------

type stunnelSvc struct {
	Name        string
	AcceptPort  int
	ConnectPort int // 127.0.0.1:<port>
	CertFile    string
	KeyFile     string
}

func (s sshSystem) writeStunnel(svcs []stunnelSvc) error {
	if len(svcs) == 0 {
		// Nothing to serve: stop the instance and remove config.
		s.stopStunnel()
		_ = os.Remove(stunnelConfPath)
		return nil
	}
	stunnelBin, err := s.tool("stunnel", "stunnel4", "/usr/bin/stunnel", "/usr/bin/stunnel4")
	if err != nil {
		return errors.New("stunnel is not installed (apt-get install -y stunnel4)")
	}
	if err := os.MkdirAll(filepath.Dir(stunnelConfPath), 0o755); err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString("; Managed by 4x-ui SSH Manager. Do not edit by hand.\n")
	sb.WriteString("foreground = yes\n")
	sb.WriteString("pid =\n")
	sb.WriteString("debug = 4\n\n")
	for _, svc := range svcs {
		sb.WriteString(fmt.Sprintf("[%s]\n", svc.Name))
		sb.WriteString(fmt.Sprintf("accept = 0.0.0.0:%d\n", svc.AcceptPort))
		sb.WriteString(fmt.Sprintf("connect = 127.0.0.1:%d\n", svc.ConnectPort))
		sb.WriteString(fmt.Sprintf("cert = %s\n", svc.CertFile))
		sb.WriteString(fmt.Sprintf("key = %s\n\n", svc.KeyFile))
	}
	if err := os.WriteFile(stunnelConfPath, []byte(sb.String()), 0o644); err != nil {
		return err
	}

	// Dedicated systemd unit running our config in the foreground.
	unit := fmt.Sprintf(`[Unit]
Description=4x-ui SSH Manager stunnel
After=network.target

[Service]
Type=simple
ExecStart=%s %s
Restart=on-failure
RestartSec=3s

[Install]
WantedBy=multi-user.target
`, stunnelBin, stunnelConfPath)
	if err := os.WriteFile(stunnelUnitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	systemctl, err := s.tool("systemctl", "/usr/bin/systemctl", "/bin/systemctl")
	if err != nil {
		return err
	}
	_, _ = s.run(systemctl, "daemon-reload")
	_, _ = s.run(systemctl, "enable", "xui-stunnel.service")
	if out, err := s.run(systemctl, "restart", "xui-stunnel.service"); err != nil {
		return fmt.Errorf("restart xui-stunnel: %s", strings.TrimSpace(out))
	}
	logger.Infof("ssh-manager: stunnel reconciled with %d service(s)", len(svcs))
	return nil
}

func (s sshSystem) stopStunnel() {
	if systemctl, err := s.tool("systemctl", "/usr/bin/systemctl", "/bin/systemctl"); err == nil {
		_, _ = s.run(systemctl, "stop", "xui-stunnel.service")
	}
}

func (s sshSystem) stunnelInstalled() bool {
	_, err := s.tool("stunnel", "stunnel4", "/usr/bin/stunnel", "/usr/bin/stunnel4")
	return err == nil
}

// writeBanner persists a banner file for an inbound and returns its path.
// (Wired into sshd via the Banner directive only when non-empty; banners are
// sent before authentication and never interfere with the SSH tunnel.)
func (s sshSystem) writeBanner(id int, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	if err := os.MkdirAll(bannerDir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(bannerDir, fmt.Sprintf("ssh-%d.txt", id))
	if err := os.WriteFile(p, []byte(text+"\n"), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// AES-GCM password-at-rest (keyed from the panel secret)
// ---------------------------------------------------------------------------

func deriveKey(secret string) []byte {
	sum := sha256.Sum256([]byte("xui-ssh-manager|" + secret))
	return sum[:]
}

// msToChageDate converts a unix-ms expiry to the YYYY-MM-DD string chage wants,
// returning "" for "never".
func msToChageDate(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02")
}

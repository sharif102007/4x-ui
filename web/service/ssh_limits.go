package service

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sharif102007/4x-ui/v2/database"
	"github.com/sharif102007/4x-ui/v2/database/model"
	"github.com/sharif102007/4x-ui/v2/logger"

	"gorm.io/gorm"
)

const sshNftTable = "fourxui_ssh"

const (
	sshSessionLimitConfig = "/etc/security/4xui-ssh-session-limits.conf"
	sshSessionLimitScript = "/usr/local/x-ui/bin/ssh-session-limit"
	sshSessionPAMMarker   = "# 4x-ui SSH concurrent-session limit"
)

var (
	sshLimitStop              chan struct{}
	sshLimitWG                sync.WaitGroup
	sshCounterMu              sync.Mutex
	sshLastBytes              = map[int]int64{}
	sshPolicyMu               sync.Mutex
	sshPolicySignature        string
	sshSessionPolicyMu        sync.Mutex
	sshSessionPolicySignature string
	nftBytesPattern           = regexp.MustCompile(`\bbytes\s+([0-9]+)\b`)
)

func validResetFlow(v string) bool {
	switch v {
	case "", "never", "daily", "weekly", "monthly":
		return true
	}
	return false
}

func validateUserLimits(u *model.SshUser) error {
	if u.TrafficLimit < 0 {
		return errors.New("traffic limit cannot be negative")
	}
	if !validResetFlow(u.ResetFlow) {
		return errors.New("invalid reset flow")
	}
	if u.DownloadMbps < 0 || u.UploadMbps < 0 {
		return errors.New("speed cannot be negative")
	}
	if u.DownloadMbps > 100000 || u.UploadMbps > 100000 {
		return errors.New("speed limit is too large")
	}
	if u.MaxSessions < 0 || u.MaxSessions > 10000 {
		return errors.New("concurrent session limit must be between 0 and 10000")
	}
	if u.ResetFlow == "" {
		u.ResetFlow = "never"
	}
	if !u.SpeedLimit {
		u.DownloadMbps, u.UploadMbps = 0, 0
	}
	return nil
}

func (s *SshManagerService) ResetUserTraffic(id int) error {
	now := time.Now().UnixMilli()
	if err := database.GetDB().Model(&model.SshUser{}).Where("id = ?", id).Updates(map[string]any{"traffic_used": 0, "last_reset_time": now}).Error; err != nil {
		return err
	}
	sshCounterMu.Lock()
	delete(sshLastBytes, id)
	sshCounterMu.Unlock()
	sshPolicyMu.Lock()
	sshPolicySignature = ""
	sshPolicyMu.Unlock()
	return nil
}

func shouldReset(u *model.SshUser, now time.Time) bool {
	if u.ResetFlow == "" || u.ResetFlow == "never" {
		return false
	}
	if u.LastResetTime == 0 {
		return true
	}
	last := time.UnixMilli(u.LastResetTime)
	switch u.ResetFlow {
	case "daily":
		return now.YearDay() != last.YearDay() || now.Year() != last.Year()
	case "weekly":
		y, w := now.ISOWeek()
		ly, lw := last.ISOWeek()
		return y != ly || w != lw
	case "monthly":
		return now.Year() != last.Year() || now.Month() != last.Month()
	}
	return false
}

func userUID(name string) (int, error) {
	out, err := exec.Command("id", "-u", name).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("lookup uid for %s: %w (%s)", name, err, strings.TrimSpace(string(out)))
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

func sshMark(id int) int { return 0x100000 + id }
func sshCounterName(id int, direction string) string {
	return fmt.Sprintf("user_%d_%s", id, direction)
}

func rateBytesPerSecond(mbps int) int64 {
	if mbps <= 0 {
		return 0
	}
	return int64(mbps) * 1000 * 1000 / 8
}

func nftRateRule(mark, mbps int, counter string) string {
	if mbps <= 0 {
		return ""
	}
	rate := rateBytesPerSecond(mbps)
	burst := rate / 2
	if burst < 65536 {
		burst = 65536
	}
	counterExpr := ""
	if counter != "" {
		counterExpr = " counter name " + counter
	}
	return fmt.Sprintf("    meta mark %d limit rate over %d bytes/second burst %d bytes%s drop\n", mark, rate, burst, counterExpr)
}

func applyNftTable(name, script string) error {
	if _, err := exec.LookPath("nft"); err != nil {
		return errors.New("nft command is missing; install nftables")
	}
	checkName := name + "_check"
	checkScript := strings.Replace(script, "table inet "+name, "table inet "+checkName, 1)
	check := exec.Command("nft", "-c", "-f", "-")
	check.Stdin = strings.NewReader(checkScript)
	if out, err := check.CombinedOutput(); err != nil {
		return fmt.Errorf("invalid nft policy: %w: %s", err, strings.TrimSpace(string(out)))
	}
	_ = exec.Command("nft", "delete", "table", "inet", name).Run()
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft apply failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func buildSSHPolicy(users []model.SshUser) (string, string, error) {
	sort.Slice(users, func(i, j int) bool { return users[i].Id < users[j].Id })
	var declarations, outputRules, inputRules strings.Builder
	var signature strings.Builder
	for i := range users {
		u := users[i]
		uid, err := userUID(u.Username)
		if err != nil {
			logger.Warningf("ssh-manager: %v", err)
			continue
		}
		upCounter := sshCounterName(u.Id, "up")
		downCounter := sshCounterName(u.Id, "down")
		mark := sshMark(u.Id)
		fmt.Fprintf(&signature, "%d:%d:%t:%d:%d;", u.Id, uid, u.SpeedLimit, u.DownloadMbps, u.UploadMbps)
		fmt.Fprintf(&declarations, "  counter %s {}\n  counter %s {}\n", upCounter, downCounter)
		fmt.Fprintf(&outputRules, "    meta skuid %d meta mark set %d\n", uid, mark)
		fmt.Fprintf(&outputRules, "    meta mark %d ct mark set meta mark counter name %s\n", mark, upCounter)
		if u.SpeedLimit {
			outputRules.WriteString(nftRateRule(mark, u.UploadMbps, ""))
		}
		fmt.Fprintf(&inputRules, "    meta mark %d counter name %s\n", mark, downCounter)
		if u.SpeedLimit {
			inputRules.WriteString(nftRateRule(mark, u.DownloadMbps, ""))
		}
	}
	sig := fmt.Sprintf("%x", sha256.Sum256([]byte(signature.String())))
	script := fmt.Sprintf("table inet %s {\n%s  chain output {\n    type filter hook output priority mangle; policy accept;\n%s  }\n  chain prerouting {\n    type filter hook prerouting priority mangle; policy accept;\n    ct mark != 0 meta mark set ct mark\n%s  }\n}\n", sshNftTable, declarations.String(), outputRules.String(), inputRules.String())
	return sig, script, nil
}

func ensureSSHPolicy(users []model.SshUser) bool {
	sig, script, err := buildSSHPolicy(users)
	if err != nil {
		logger.Warningf("ssh-manager: build bandwidth policy: %v", err)
		return false
	}
	sshPolicyMu.Lock()
	defer sshPolicyMu.Unlock()
	if sig == sshPolicySignature {
		return false
	}
	if err := applyNftTable(sshNftTable, script); err != nil {
		logger.Warningf("ssh-manager: bandwidth/traffic policy unavailable: %v", err)
		return false
	}
	sshPolicySignature = sig
	sshCounterMu.Lock()
	sshLastBytes = map[int]int64{}
	sshCounterMu.Unlock()
	logger.Infof("ssh-manager: applied traffic and speed policy for %d users", len(users))
	return true
}

// ensureSSHSessionLimitConfig installs a small PAM gate for managed SSH users.
// PAM runs this before the authenticated session is opened, which lets us
// reject a new login without disconnecting existing sessions. Users not listed
// in the managed config are always allowed, so the VPS administrator account is
// never affected by this feature.
func ensureSSHSessionLimitConfig(users []model.SshUser) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	var config bytes.Buffer
	var signature bytes.Buffer
	for _, u := range users {
		if u.MaxSessions <= 0 {
			continue
		}
		fmt.Fprintf(&config, "%s %d\n", u.Username, u.MaxSessions)
		fmt.Fprintf(&signature, "%s:%d;", u.Username, u.MaxSessions)
	}
	sig := fmt.Sprintf("%x", sha256.Sum256(signature.Bytes()))
	sshSessionPolicyMu.Lock()
	defer sshSessionPolicyMu.Unlock()
	if sig == sshSessionPolicySignature {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(sshSessionLimitConfig), 0o755); err != nil {
		logger.Warningf("ssh-manager: create session-limit directory: %v", err)
		return false
	}
	if err := os.WriteFile(sshSessionLimitConfig, config.Bytes(), 0o600); err != nil {
		logger.Warningf("ssh-manager: write session-limit config: %v", err)
		return false
	}
	const script = `#!/bin/sh
# Managed by 4x-ui SSH Manager. Exit 0 for users without a configured limit.
set -eu
CONFIG=/etc/security/4xui-ssh-session-limits.conf
USER_NAME=${PAM_USER:-}
[ -n "$USER_NAME" ] || exit 0
[ -r "$CONFIG" ] || exit 0
LIMIT=$(awk -v user="$USER_NAME" '$1 == user { print $2; exit }' "$CONFIG")
case "$LIMIT" in
  ''|0) exit 0 ;;
  *[!0-9]*) exit 0 ;;
esac
COUNT=$(ps -eo user=,comm= 2>/dev/null | awk -v user="$USER_NAME" '$1 == user && ($2 == "sshd" || $2 == "sshd-session") { n++ } END { print n + 0 }')
if [ "$COUNT" -ge "$LIMIT" ]; then
  echo "4x-ui: concurrent SSH session limit reached" >&2
  exit 1
fi
exit 0
`
	if err := os.MkdirAll(filepath.Dir(sshSessionLimitScript), 0o755); err != nil {
		logger.Warningf("ssh-manager: create session-limit script directory: %v", err)
		return false
	}
	if err := os.WriteFile(sshSessionLimitScript, []byte(script), 0o700); err != nil {
		logger.Warningf("ssh-manager: write session-limit script: %v", err)
		return false
	}
	_ = os.Chmod(sshSessionLimitScript, 0o700)

	pamPath := "/etc/pam.d/sshd"
	pam, err := os.ReadFile(pamPath)
	if err != nil {
		logger.Warningf("ssh-manager: read %s for session limit: %v", pamPath, err)
		return false
	}
	if !bytes.Contains(pam, []byte(sshSessionPAMMarker)) {
		pamExec := false
		for _, pattern := range []string{"/lib/*/security/pam_exec.so", "/usr/lib/*/security/pam_exec.so"} {
			if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
				pamExec = true
				break
			}
		}
		if !pamExec {
			logger.Warning("ssh-manager: pam_exec.so is unavailable; SSH session limit is not active")
			return false
		}
		addition := []byte("\n" + sshSessionPAMMarker + "\nauth requisite pam_exec.so quiet " + sshSessionLimitScript + "\n")
		if err := os.WriteFile(pamPath, append(pam, addition...), 0o644); err != nil {
			logger.Warningf("ssh-manager: install PAM session-limit hook: %v", err)
			return false
		}
	}
	sshSessionPolicySignature = sig
	if config.Len() > 0 {
		logger.Infof("ssh-manager: applied concurrent-session limits for managed users")
	} else {
		logger.Infof("ssh-manager: concurrent-session limits disabled")
	}
	return true
}

func readNftCounter(name string) int64 {
	out, err := exec.Command("nft", "list", "counter", "inet", sshNftTable, name).CombinedOutput()
	if err != nil {
		return 0
	}
	m := nftBytesPattern.FindStringSubmatch(string(out))
	if len(m) != 2 {
		return 0
	}
	v, _ := strconv.ParseInt(m[1], 10, 64)
	return v
}

func readSSHBytes(id int) int64 {
	return readNftCounter(sshCounterName(id, "up")) + readNftCounter(sshCounterName(id, "down"))
}

func (s *SshManagerService) reconcileUserLimits() {
	var users []model.SshUser
	if err := database.GetDB().Find(&users).Error; err != nil {
		logger.Warningf("ssh-manager: load limit users: %v", err)
		return
	}
	now := time.Now()
	for i := range users {
		if shouldReset(&users[i], now) {
			_ = s.ResetUserTraffic(users[i].Id)
			users[i].TrafficUsed = 0
			users[i].LastResetTime = now.UnixMilli()
		}
	}
	ensureSSHSessionLimitConfig(users)
	ensureSSHPolicy(users)
	for i := range users {
		u := &users[i]
		total := readSSHBytes(u.Id)
		sshCounterMu.Lock()
		prev, ok := sshLastBytes[u.Id]
		sshLastBytes[u.Id] = total
		sshCounterMu.Unlock()
		if ok && total >= prev {
			delta := total - prev
			if delta > 0 {
				if err := database.GetDB().Model(&model.SshUser{}).Where("id = ?", u.Id).UpdateColumn("traffic_used", gorm.Expr("traffic_used + ?", delta)).Error; err != nil {
					logger.Warningf("ssh-manager: save traffic for %s: %v", u.Username, err)
				}
				u.TrafficUsed += delta
			}
		}
		if u.Enable && u.TrafficLimit > 0 && u.TrafficUsed >= u.TrafficLimit {
			if err := s.SetUserEnable(u.Id, false); err == nil {
				logger.Infof("ssh-manager: disabled %s after traffic limit", u.Username)
			} else {
				logger.Warningf("ssh-manager: disable %s after traffic limit: %v", u.Username, err)
			}
		}
	}
}

func (s *SshManagerService) startLimitRuntime() {
	if sshLimitStop != nil {
		return
	}
	sshLimitStop = make(chan struct{})
	sshLimitWG.Add(1)
	go func() {
		defer sshLimitWG.Done()
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		s.reconcileUserLimits()
		for {
			select {
			case <-t.C:
				s.reconcileUserLimits()
			case <-sshLimitStop:
				return
			}
		}
	}()
}

func stopLimitRuntime() {
	if sshLimitStop != nil {
		close(sshLimitStop)
		sshLimitWG.Wait()
		sshLimitStop = nil
	}
}

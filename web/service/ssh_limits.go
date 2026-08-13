package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
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

var (
	sshLimitStop       chan struct{}
	sshLimitWG         sync.WaitGroup
	sshCounterMu       sync.Mutex
	sshLastBytes       = map[int]int64{}
	sshPolicyMu        sync.Mutex
	sshPolicySignature string
	sshRateStateReady  bool
	// One table dump contains every named counter. The old implementation ran
	// two `nft` processes per user every five seconds, which caused periodic CPU
	// and I/O spikes on small VPS instances.
	nftCounterPattern = regexp.MustCompile(`(?s)\bcounter\s+user_([0-9]+)_(?:up|down)\s*\{[^}]*?\bbytes\s+([0-9]+)\b`)
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
	result := database.GetDB().Model(&model.SshUser{}).Where("id = ?", id).Updates(map[string]any{"traffic_used": 0, "last_reset_time": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("user not found")
	}
	sshCounterMu.Lock()
	delete(sshLastBytes, id)
	sshCounterMu.Unlock()
	sshPolicyMu.Lock()
	sshPolicySignature = ""
	sshRateStateReady = false
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
	// This rule is a safe fallback until HTB/IFB is healthy. A two-second
	// bucket avoids punishing harmless TCP bursts with loss/retransmission while
	// still enforcing the configured sustained rate if queue shaping is absent.
	burst := rate * 2
	if burst < 1024*1024 {
		burst = 1024 * 1024
	}
	if burst > 32*1024*1024 {
		burst = 32 * 1024 * 1024
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
	applyScript := script
	hadTable := nftTableExists(name)
	if hadTable {
		// nft executes a file as one netlink batch. Delete + recreate together so
		// a runtime failure rolls the whole change back and preserves the old
		// working policy instead of leaving the host with no table.
		applyScript = fmt.Sprintf("delete table inet %s\n%s", name, script)
	}
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(applyScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// A firewall reload may remove the table between the existence probe and
		// the atomic batch. In that one race, retry creation without delete.
		if hadTable && !nftTableExists(name) {
			retry := exec.Command("nft", "-f", "-")
			retry.Stdin = strings.NewReader(script)
			if retryOut, retryErr := retry.CombinedOutput(); retryErr == nil {
				return nil
			} else {
				return fmt.Errorf("nft apply failed after table disappeared: %w: %s", retryErr, strings.TrimSpace(string(retryOut)))
			}
		}
		return fmt.Errorf("nft apply failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func nftTableExists(name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "nft", "list", "table", "inet", name).Run() == nil
}

func buildSSHPolicy(users []model.SshUser) (string, string, []shaperRate, error) {
	sort.Slice(users, func(i, j int) bool { return users[i].Id < users[j].Id })
	var declarations, outputRules, inputRules strings.Builder
	var signature strings.Builder
	rates := make([]shaperRate, 0, len(users))
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
			rates = append(rates, shaperRate{
				Mark:     mark,
				UpBits:   int64(u.UploadMbps) * 1000 * 1000,
				DownBits: int64(u.DownloadMbps) * 1000 * 1000,
			})
		}
		fmt.Fprintf(&inputRules, "    meta mark %d counter name %s\n", mark, downCounter)
		if u.SpeedLimit {
			inputRules.WriteString(nftRateRule(mark, u.DownloadMbps, ""))
		}
	}
	sig := fmt.Sprintf("%x", sha256.Sum256([]byte(signature.String())))
	script := fmt.Sprintf("table inet %s {\n%s  chain output {\n    type filter hook output priority mangle; policy accept;\n%s  }\n  chain prerouting {\n    type filter hook prerouting priority mangle; policy accept;\n    ct mark != 0 meta mark set ct mark\n%s  }\n}\n", sshNftTable, declarations.String(), outputRules.String(), inputRules.String())
	return sig, script, rates, nil
}

func ensureSSHPolicy(users []model.SshUser) bool {
	sig, script, rates, err := buildSSHPolicy(users)
	if err != nil {
		logger.Warningf("ssh-manager: build bandwidth policy: %v", err)
		return false
	}
	sshPolicyMu.Lock()
	defer sshPolicyMu.Unlock()
	if sig == sshPolicySignature && nftTableExists(sshNftTable) {
		if sshRateStateReady && shaperRateStateExists("ssh") {
			return false
		}
		sshRateStateReady = false
		if err := writeShaperRateState("ssh", rates); err != nil {
			logger.Warningf("ssh-manager: queue-rate state retry failed; using nftables policer: %v", err)
			return false
		}
		sshRateStateReady = true
		setSshSpeedLimitPresence(hasEnabledSpeedLimit(users))
		return true
	}
	if err := applyNftTable(sshNftTable, script); err != nil {
		logger.Warningf("ssh-manager: bandwidth/traffic policy unavailable: %v", err)
		return false
	}
	sshPolicySignature = sig
	sshRateStateReady = false
	sshCounterMu.Lock()
	sshLastBytes = map[int]int64{}
	sshCounterMu.Unlock()
	if err := writeShaperRateState("ssh", rates); err != nil {
		// Leave the nftables policer active when queue state cannot be
		// published. The shaper will never remove a fallback it cannot replace.
		logger.Warningf("ssh-manager: queue-rate state unavailable; using nftables policer: %v", err)
		return false
	}
	sshRateStateReady = true
	logger.Infof("ssh-manager: applied traffic and speed policy for %d users", len(users))

	// Tell the shaper whether any SSH user still needs real queueing. Counters
	// are installed for every user, so the presence of the table alone does not
	// imply a rate limit.
	setSshSpeedLimitPresence(hasEnabledSpeedLimit(users))
	return true
}

func hasEnabledSpeedLimit(users []model.SshUser) bool {
	for i := range users {
		if users[i].SpeedLimit && (users[i].DownloadMbps > 0 || users[i].UploadMbps > 0) {
			return true
		}
	}
	return false
}

func parseSSHCounterBytes(raw string) map[int]int64 {
	totals := make(map[int]int64)
	for _, match := range nftCounterPattern.FindAllStringSubmatch(raw, -1) {
		if len(match) != 3 {
			continue
		}
		id, idErr := strconv.Atoi(match[1])
		bytes, bytesErr := strconv.ParseInt(match[2], 10, 64)
		if idErr == nil && bytesErr == nil {
			totals[id] += bytes
		}
	}
	return totals
}

func readAllSSHBytes() (map[int]int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nft", "list", "table", "inet", sshNftTable).CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("nft counter read timed out: %w", ctx.Err())
	}
	if err != nil {
		return nil, err
	}
	return parseSSHCounterBytes(string(out)), nil
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
	ensureSSHPolicy(users)
	counterTotals, counterErr := readAllSSHBytes()
	for i := range users {
		u := &users[i]
		// A failed nft read must not be treated as a zero counter. Doing that
		// resets the baseline and double-counts the whole counter on the next
		// successful poll, potentially locking a user before their real quota.
		if counterErr == nil {
			total := counterTotals[u.Id]
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
		}
		if u.Enable && u.TrafficLimit > 0 && u.TrafficUsed >= u.TrafficLimit {
			if err := s.SetUserEnable(u.Id, false); err == nil {
				logger.Infof("ssh-manager: disabled %s after traffic limit", u.Username)
			} else {
				logger.Warningf("ssh-manager: disable %s after traffic limit: %v", u.Username, err)
			}
			continue
		}
		// Expiry was previously handed to the OS alone (chage), so an expired
		// account could no longer log in while the panel still displayed it as
		// Enabled - the state the operator sees disagreed with the state the
		// host enforces. Mirroring it into the panel keeps the two in step and
		// makes SetUserEnable lock the account explicitly rather than relying
		// on the shadow expiry field.
		if u.Enable && u.ExpiryTime > 0 && now.UnixMilli() >= u.ExpiryTime {
			if err := s.SetUserEnable(u.Id, false); err == nil {
				logger.Infof("ssh-manager: disabled %s after expiry", u.Username)
			} else {
				logger.Warningf("ssh-manager: disable %s after expiry: %v", u.Username, err)
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
		// One table dump every ten seconds keeps usage reasonably current without
		// adding a periodic process/DB spike to latency-sensitive small VPS hosts.
		t := time.NewTicker(10 * time.Second)
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
		waitWithTimeout(&sshLimitWG, "ssh limit poller")
		sshLimitStop = nil
	}
}

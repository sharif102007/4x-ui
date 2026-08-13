package service

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// shaperRate is the queue rate consumed by 4xui-shaper.sh. Bits per second are
// used so no precision is lost converting the panel's Mbps values.
type shaperRate struct {
	Mark     int
	UpBits   int64
	DownBits int64
}

func shaperStateDir() string {
	if dir := os.Getenv("XUI_SHAPER_STATE_DIR"); dir != "" {
		return dir
	}
	return "/run/4xui-shaper"
}

// writeShaperRateState atomically publishes one subsystem's desired queue
// rates. Keeping SSH and Xray in separate files lets either policy change
// without losing the other subsystem's rates. The nftables policer remains the
// fallback until the shell shaper confirms HTB/IFB is active.
func writeShaperRateState(scope string, rates []shaperRate) error {
	if scope != "ssh" && scope != "xray" {
		return fmt.Errorf("invalid shaper rate scope %q", scope)
	}
	dir := shaperStateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	sort.Slice(rates, func(i, j int) bool { return rates[i].Mark < rates[j].Mark })
	tmp, err := os.CreateTemp(dir, scope+".limits.tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()

	w := bufio.NewWriter(tmp)
	for _, rate := range rates {
		if rate.Mark <= 0 || (rate.UpBits <= 0 && rate.DownBits <= 0) {
			continue
		}
		if _, err := fmt.Fprintf(w, "%d %d %d\n", rate.Mark, rate.UpBits, rate.DownBits); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, scope+".limits")); err != nil {
		return err
	}
	ok = true
	return nil
}

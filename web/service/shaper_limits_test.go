package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteShaperRateStateIsSortedAndAtomic(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_SHAPER_STATE_DIR", dir)

	rates := []shaperRate{
		{Mark: 12, UpBits: 2_000_000, DownBits: 4_000_000},
		{Mark: 5, UpBits: 1_000_000},
		{Mark: 99}, // empty limits are intentionally omitted
	}
	if err := writeShaperRateState("ssh", rates); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "ssh.limits"))
	if err != nil {
		t.Fatal(err)
	}
	want := "5 1000000 0\n12 2000000 4000000\n"
	if string(raw) != want {
		t.Fatalf("rate state = %q, want %q", string(raw), want)
	}
	if info, err := os.Stat(filepath.Join(dir, "ssh.limits")); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("rate state mode = %o, want 600", info.Mode().Perm())
	}
}

func TestWriteShaperRateStateRejectsUnknownScope(t *testing.T) {
	t.Setenv("XUI_SHAPER_STATE_DIR", t.TempDir())
	if err := writeShaperRateState("other", nil); err == nil {
		t.Fatal("unknown scope was accepted")
	}
}

func TestShaperRateStateExists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_SHAPER_STATE_DIR", dir)
	if shaperRateStateExists("ssh") {
		t.Fatal("missing state file reported as present")
	}
	if err := writeShaperRateState("ssh", nil); err != nil {
		t.Fatal(err)
	}
	if !shaperRateStateExists("ssh") {
		t.Fatal("published state file was not detected")
	}
	if shaperRateStateExists("invalid") {
		t.Fatal("invalid scope reported as present")
	}
}

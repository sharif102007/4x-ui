package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/sharif102007/4x-ui/v2/util/json_util"
	"github.com/sharif102007/4x-ui/v2/xray"
)

func TestCollectXrayBandwidthLimit(t *testing.T) {
	limit, ok := collectXrayBandwidthLimit(map[string]any{
		"email":        "client@example.com",
		"speedLimit":   true,
		"downloadMbps": float64(8),
		"uploadMbps":   float64(3),
	})
	if !ok || limit.Email != "client@example.com" || limit.DownloadMbps != 8 || limit.UploadMbps != 3 {
		t.Fatalf("unexpected limit: %#v, ok=%v", limit, ok)
	}
}

func TestXraySpeedConfigAddsMarkedOutboundAndUserRule(t *testing.T) {
	config := &xray.Config{
		OutboundConfigs: json_util.RawMessage(`[{"tag":"direct","protocol":"freedom","settings":{}}]`),
		RouterConfig:    json_util.RawMessage(`{"domainStrategy":"AsIs","rules":[]}`),
	}
	limits := []xrayBandwidthLimit{{Email: "limited", DownloadMbps: 2, UploadMbps: 1}}
	if err := injectXrayBandwidthConfig(config, limits); err != nil {
		t.Fatal(err)
	}
	var outbounds []map[string]any
	if err := json.Unmarshal(config.OutboundConfigs, &outbounds); err != nil {
		t.Fatal(err)
	}
	if len(outbounds) != 2 {
		t.Fatalf("expected original and limited outbound, got %d", len(outbounds))
	}
	mark := limits[0].Mark
	if mark == 0 {
		t.Fatal("mark was not assigned")
	}
	wantTag := fmt.Sprintf("%s%x", xrayLimitTag, mark)
	if outbounds[1]["tag"] != wantTag {
		t.Fatalf("unexpected limited outbound tag: %v", outbounds[1]["tag"])
	}
	var routing map[string]any
	if err := json.Unmarshal(config.RouterConfig, &routing); err != nil {
		t.Fatal(err)
	}
	rules := routing["rules"].([]any)
	first := rules[0].(map[string]any)
	if first["outboundTag"] != wantTag {
		t.Fatalf("user rule does not target limited outbound: %#v", first)
	}
	_, script := buildXrayPolicy(limits)
	if !strings.Contains(script, "socket mark != 0 meta mark set socket mark") ||
		!strings.Contains(script, "meta mark ") ||
		!strings.Contains(script, "250000 bytes/second") ||
		!strings.Contains(script, "125000 bytes/second") {
		t.Fatalf("unexpected nft policy:\n%s", script)
	}
}

func TestCollectXrayBandwidthLimitAcceptsPersistedStrings(t *testing.T) {
	limit, ok := collectXrayBandwidthLimit(map[string]any{
		"email":        "legacy@example.com",
		"speedLimit":   true,
		"downloadMbps": "8",
		"uploadMbps":   "3",
	})
	if !ok || limit.DownloadMbps != 8 || limit.UploadMbps != 3 {
		t.Fatalf("string rates were not parsed: %#v, ok=%v", limit, ok)
	}
}

func TestRateConversionUsesMegabits(t *testing.T) {
	if got := rateBytesPerSecond(2); got != 250000 {
		t.Fatalf("2 Mbps = 250000 bytes/s, got %d", got)
	}
}

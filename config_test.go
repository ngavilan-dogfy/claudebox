package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyConfFile(t *testing.T) {
	os.Setenv("CBOX_TEST_VAL", "/data")
	dir := t.TempDir()
	conf := filepath.Join(dir, ".cbox.conf")
	os.WriteFile(conf, []byte(`
# comment
CBOX_NET="allowlist"
export CBOX_SSH='agent'
CBOX_PORTS="3000 5173"
CBOX_MOUNTS="$CBOX_TEST_VAL:/data:ro"
CBOX_UNKNOWN_KEY="x"
not a conf line
`), 0o644)

	cfg := &Config{}
	cfg.applyConfFile(conf)
	if cfg.Net != "allowlist" {
		t.Errorf("Net = %q", cfg.Net)
	}
	if cfg.SSH != "agent" {
		t.Errorf("SSH = %q (export prefix should work)", cfg.SSH)
	}
	if len(cfg.Ports) != 2 || cfg.Ports[1] != "5173" {
		t.Errorf("Ports = %v", cfg.Ports)
	}
	if len(cfg.Mounts) != 1 || cfg.Mounts[0] != "/data:/data:ro" {
		t.Errorf("Mounts = %v (env expansion)", cfg.Mounts)
	}
}

func TestVolumeName(t *testing.T) {
	cases := []struct {
		env, custom, want string
	}{
		{"", "", "claude-box-config"},
		{"work", "", "claude-box-config-work"},
		{"work", "explicit-vol", "explicit-vol"},
	}
	for _, c := range cases {
		cfg := &Config{Env: c.env, ConfigVolume: c.custom}
		if got := cfg.Volume(); got != c.want {
			t.Errorf("Volume(env=%q custom=%q) = %q, want %q", c.env, c.custom, got, c.want)
		}
	}
}

func TestImageTag(t *testing.T) {
	cfg := &Config{ImageBase: "claude-box"}
	tag := cfg.ImageTag()
	if len(tag) != len("claude-box:h")+12 {
		t.Errorf("ImageTag = %q, want 12-hex content hash", tag)
	}
	if tag != cfg.ImageTag() {
		t.Error("ImageTag must be deterministic")
	}
}

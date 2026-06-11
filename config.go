package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	Runtime        string
	ImageBase      string
	Env            string
	Net            string // open | allowlist | full
	SSH            string // key | agent | none
	Key            string
	GitConfig      string
	Memory         string
	CPUs           string
	Pids           string
	Ports          []string
	Mounts         []string
	AllowedDomains string
	ConfigVolume   string
	AllowHome      bool
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func loadConfig() *Config {
	home, _ := os.UserHomeDir()
	cfg := &Config{
		Runtime:        envOr("CBOX_RUNTIME", "docker"),
		ImageBase:      envOr("CBOX_IMAGE", "claude-box"),
		Env:            os.Getenv("CBOX_ENV"),
		Net:            envOr("CBOX_NET", "open"),
		SSH:            envOr("CBOX_SSH", "key"),
		Key:            envOr("CBOX_KEY", filepath.Join(home, ".ssh", "claude_agent")),
		GitConfig:      envOr("CBOX_GITCONFIG", filepath.Join(home, ".config", "cbox", "gitconfig")),
		Memory:         envOr("CBOX_MEMORY", "8g"),
		CPUs:           os.Getenv("CBOX_CPUS"),
		Pids:           envOr("CBOX_PIDS", "2000"),
		Ports:          strings.Fields(os.Getenv("CBOX_PORTS")),
		Mounts:         strings.Fields(os.Getenv("CBOX_MOUNTS")),
		AllowedDomains: os.Getenv("CBOX_ALLOWED_DOMAINS"),
		ConfigVolume:   os.Getenv("CBOX_CONFIG_VOLUME"),
		AllowHome:      os.Getenv("CBOX_ALLOW_HOME") == "1",
	}
	// Global machine config first, project .cbox.conf overrides it.
	cfg.applyConfFile(filepath.Join(home, ".config", "cbox", "config"))
	cfg.applyConfFile(".cbox.conf")
	return cfg
}

var confLine = regexp.MustCompile(`^\s*(?:export\s+)?(CBOX_[A-Z_]+)\s*=\s*(.*?)\s*$`)

func (c *Config) applyConfFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		m := confLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		key, val := m[1], strings.Trim(m[2], `"'`)
		val = os.ExpandEnv(val)
		switch key {
		case "CBOX_RUNTIME":
			c.Runtime = val
		case "CBOX_IMAGE":
			c.ImageBase = val
		case "CBOX_ENV":
			c.Env = val
		case "CBOX_NET":
			c.Net = val
		case "CBOX_SSH":
			c.SSH = val
		case "CBOX_KEY":
			c.Key = val
		case "CBOX_GITCONFIG":
			c.GitConfig = val
		case "CBOX_MEMORY":
			c.Memory = val
		case "CBOX_CPUS":
			c.CPUs = val
		case "CBOX_PIDS":
			c.Pids = val
		case "CBOX_PORTS":
			c.Ports = strings.Fields(val)
		case "CBOX_MOUNTS":
			c.Mounts = strings.Fields(val)
		case "CBOX_ALLOWED_DOMAINS":
			c.AllowedDomains = val
		case "CBOX_CONFIG_VOLUME":
			c.ConfigVolume = val
		case "CBOX_ALLOW_HOME":
			c.AllowHome = val == "1"
		default:
			fmt.Fprintln(os.Stderr, dim("cbox: ignoring unknown key in .cbox.conf: "+key))
		}
	}
}

func (c *Config) EnvName() string {
	if c.Env == "" {
		return "default"
	}
	return c.Env
}

func (c *Config) Volume() string {
	if c.ConfigVolume != "" {
		return c.ConfigVolume
	}
	if c.Env != "" {
		return "claude-box-config-" + c.Env
	}
	return "claude-box-config"
}

func (c *Config) ImageTag() string {
	return c.ImageBase + ":h" + assetsHash()
}

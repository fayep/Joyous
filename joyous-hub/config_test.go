package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfigPath(t *testing.T) {
	path, err := DefaultConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, filepath.Join("Joyous", "config.yaml")) {
		t.Fatalf("unexpected path suffix: %q", path)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, dir) {
		t.Fatalf("path %q should be under %q", path, dir)
	}
}

func TestLoadHubConfigMissingFile(t *testing.T) {
	cfg, err := LoadHubConfig(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	def := defaultHubConfig()
	if cfg != def {
		t.Fatalf("got %+v, want defaults %+v", cfg, def)
	}
}

func TestLoadHubConfigYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	const content = `
listen_mqtt: ":11883"
listen_http: ":18080"
upstream: ""
upstream_usr: alice
upstream_pwd: secret
data_dir: /data/frames
server_addr: hub.local:18080
discover_subnets: 192.168.50
log_dir: /var/log/joyous
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadHubConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenMQTT != ":11883" || cfg.ListenHTTP != ":18080" {
		t.Fatalf("listen ports: mqtt=%q http=%q", cfg.ListenMQTT, cfg.ListenHTTP)
	}
	if cfg.Upstream != "" || cfg.UpstreamUsr != "alice" || cfg.UpstreamPwd != "secret" {
		t.Fatalf("upstream: %+v", cfg)
	}
	if cfg.DataDir != "/data/frames" || cfg.ServerAddr != "hub.local:18080" {
		t.Fatalf("paths: data=%q server=%q", cfg.DataDir, cfg.ServerAddr)
	}
	if cfg.DiscoverSubnets != "192.168.50" || cfg.LogDir != "/var/log/joyous" {
		t.Fatalf("discovery/log: subnets=%q log=%q", cfg.DiscoverSubnets, cfg.LogDir)
	}
}

func TestConfigPathFromArgs(t *testing.T) {
	args := []string{"joyous-hub", "--config", "/tmp/custom.yaml", "--listen-http=:9000"}
	if got := configPathFromArgs(args); got != "/tmp/custom.yaml" {
		t.Fatalf("got %q", got)
	}
	if got := configPathFromArgs([]string{"joyous-hub", "--config=/other.yaml"}); got != "/other.yaml" {
		t.Fatalf("got %q", got)
	}
	if got := configPathFromArgs([]string{"joyous-hub"}); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	t.Setenv("INKJOY_MQTT_USER", "from-env")
	t.Setenv("INKJOY_MQTT_PASSWORD", "env-pass")
	cfg := HubConfig{UpstreamUsr: "", UpstreamPwd: ""}
	applyEnvOverrides(&cfg)
	if cfg.UpstreamUsr != "from-env" || cfg.UpstreamPwd != "env-pass" {
		t.Fatalf("got usr=%q pwd=%q", cfg.UpstreamUsr, cfg.UpstreamPwd)
	}
	cfg = HubConfig{UpstreamUsr: "yaml-user", UpstreamPwd: ""}
	applyEnvOverrides(&cfg)
	if cfg.UpstreamUsr != "yaml-user" {
		t.Fatalf("yaml user overwritten: %q", cfg.UpstreamUsr)
	}
	if cfg.UpstreamPwd != "env-pass" {
		t.Fatalf("empty yaml pwd not filled from env: %q", cfg.UpstreamPwd)
	}
}

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
listen_mqtt: ":1883"
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
	if cfg.ListenMQTT != ":1883" || cfg.ListenHTTP != ":18080" {
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

func TestDefaultInkJoyConfigPath(t *testing.T) {
	path, err := DefaultInkJoyConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, filepath.Join("Joyous", "inkjoy-config.yaml")) {
		t.Fatalf("unexpected path suffix: %q", path)
	}
}

func TestLoadInkJoyBridgeConfigYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inkjoy-config.yaml")
	const content = `
hub_mqtt: "tcp://127.0.0.1:1999"
listen_mqtt: ":11883"
upstream: "cloud.example:1883"
upstream_usr: alice
upstream_pwd: secret
upstream_allow: "login,heart"
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadInkJoyBridgeConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HubMQTT != "tcp://127.0.0.1:1999" || cfg.ListenMQTT != ":11883" {
		t.Fatalf("listen: hub=%q mqtt=%q", cfg.HubMQTT, cfg.ListenMQTT)
	}
	if cfg.Upstream != "cloud.example:1883" || cfg.UpstreamUsr != "alice" {
		t.Fatalf("upstream: %+v", cfg)
	}
	if cfg.UpstreamAllow != "login,heart" {
		t.Fatalf("upstream_allow: %q", cfg.UpstreamAllow)
	}
}

func TestReconcileHubDataDirWithHubConfig(t *testing.T) {
	hubData := "/Volumes/tank/Media/PhotoFrame"
	bridgeData := "/Volumes/tank/Media/photoframe"
	got, changed := reconcileHubDataDirWithHubConfig(hubData, bridgeData)
	if !changed || got != hubData {
		t.Fatalf("got %q changed=%v want %q changed=true", got, changed, hubData)
	}
	got, changed = reconcileHubDataDirWithHubConfig(hubData, hubData)
	if changed || got != hubData {
		t.Fatalf("matching dirs: got %q changed=%v", got, changed)
	}
	got, changed = reconcileHubDataDirWithHubConfig(hubData, "")
	if changed || got != hubData {
		t.Fatalf("unset bridge dir: got %q changed=%v", got, changed)
	}
}

func TestDataDirsEquivalentCase(t *testing.T) {
	dir := t.TempDir()
	if !dataDirsEquivalent(dir, dir) {
		t.Fatal("same path should match")
	}
	if dataDirsEquivalent(dir, dir+"/missing") {
		t.Fatal("missing path should not match")
	}
}

func TestApplyInkJoyEnvOverrides(t *testing.T) {
	t.Setenv("INKJOY_MQTT_USER", "from-env")
	t.Setenv("INKJOY_MQTT_PASSWORD", "env-pass")
	cfg := InkJoyBridgeConfig{}
	applyInkJoyEnvOverrides(&cfg)
	if cfg.UpstreamUsr != "from-env" || cfg.UpstreamPwd != "env-pass" {
		t.Fatalf("got usr=%q pwd=%q", cfg.UpstreamUsr, cfg.UpstreamPwd)
	}
}

package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"joyous-hub/inkjoybridge"
)

// InkJoyBridgeConfig holds inkjoy-bridge settings from inkjoy-config.yaml.
type InkJoyBridgeConfig struct {
	HubMQTT         string `yaml:"hub_mqtt"`
	BridgeID        string `yaml:"bridge_id"`
	ListenMQTT      string `yaml:"listen_mqtt"`
	Upstream        string `yaml:"upstream"`
	UpstreamUsr     string `yaml:"upstream_usr"`
	UpstreamPwd     string `yaml:"upstream_pwd"`
	UpstreamAllow   string `yaml:"upstream_allow"`
	DownstreamAllow string `yaml:"downstream_allow"`
	Intercept       string `yaml:"intercept"`
	DataDir         string `yaml:"data_dir"`
	HubDataDir      string `yaml:"hub_data_dir"`
	HubHTTP         string `yaml:"hub_http"`
	ServerAddr      string `yaml:"server_addr"`
	CaptureDir      string `yaml:"capture_dir"`
	OTADir          string `yaml:"ota_dir"`
	LogDir          string `yaml:"log_dir"`
}

// SamsungBridgeConfig holds samsung-bridge settings from samsung-config.yaml.
type SamsungBridgeConfig struct {
	HubMQTT         string `yaml:"hub_mqtt"`
	BridgeID        string `yaml:"bridge_id"`
	HubHTTP         string `yaml:"hub_http"`
	ServerAddr      string `yaml:"server_addr"`
	DataDir         string `yaml:"data_dir"`
	HubDataDir      string `yaml:"hub_data_dir"`
	DiscoverSubnets string `yaml:"discover_subnets"`
	LogDir          string `yaml:"log_dir"`
}

// NixplayBridgeConfig holds nixplay-bridge settings from nixplay-config.yaml.
type NixplayBridgeConfig struct {
	HubMQTT         string `yaml:"hub_mqtt"`
	BridgeID        string `yaml:"bridge_id"`
	HubHTTP         string `yaml:"hub_http"`
	DataDir         string `yaml:"data_dir"`
	KeychainService string `yaml:"keychain_service"`
	KeychainAccount string `yaml:"keychain_account"` // Nixplay account email; also the Keychain "account"
	LogDir          string `yaml:"log_dir"`
}

func defaultInkJoyBridgeConfig() InkJoyBridgeConfig {
	return InkJoyBridgeConfig{
		HubMQTT:         fmt.Sprintf("tcp://127.0.0.1:%d", DefaultJoyousHubMQTTPort),
		ListenMQTT:      fmt.Sprintf(":%d", DefaultInkJoyFrameMQTTPort),
		HubHTTP:         fmt.Sprintf("http://127.0.0.1:%d", 18080),
		Upstream:        "13.39.148.101:1883",
		UpstreamAllow:   inkjoybridge.DefaultUpstreamAllowCSV(),
		DownstreamAllow: inkjoybridge.DefaultDownstreamAllowCSV(),
		Intercept:       inkjoybridge.DefaultInterceptCSV(),
		DataDir:         "./data-inkjoy",
	}
}

func defaultSamsungBridgeConfig() SamsungBridgeConfig {
	return SamsungBridgeConfig{
		HubMQTT: fmt.Sprintf("tcp://127.0.0.1:%d", DefaultJoyousHubMQTTPort),
		HubHTTP: fmt.Sprintf("http://127.0.0.1:%d", 18080),
		DataDir: "./data-samsung",
	}
}

func defaultNixplayBridgeConfig() NixplayBridgeConfig {
	return NixplayBridgeConfig{
		HubMQTT:         fmt.Sprintf("tcp://127.0.0.1:%d", DefaultJoyousHubMQTTPort),
		HubHTTP:         fmt.Sprintf("http://127.0.0.1:%d", 18080),
		DataDir:         "./data-nixplay",
		KeychainService: "joyous-hub-nixplay",
	}
}

// DefaultInkJoyConfigPath returns inkjoy-config.yaml next to config.yaml.
func DefaultInkJoyConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Joyous", "inkjoy-config.yaml"), nil
}

// DefaultSamsungConfigPath returns samsung-config.yaml next to config.yaml.
func DefaultSamsungConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Joyous", "samsung-config.yaml"), nil
}

// DefaultNixplayConfigPath returns nixplay-config.yaml next to config.yaml.
func DefaultNixplayConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Joyous", "nixplay-config.yaml"), nil
}

// LoadInkJoyBridgeConfig reads YAML from path. A missing file returns defaults and no error.
func LoadInkJoyBridgeConfig(path string) (InkJoyBridgeConfig, error) {
	cfg := defaultInkJoyBridgeConfig()
	if strings.TrimSpace(path) == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// LoadSamsungBridgeConfig reads YAML from path. A missing file returns defaults and no error.
func LoadSamsungBridgeConfig(path string) (SamsungBridgeConfig, error) {
	cfg := defaultSamsungBridgeConfig()
	if strings.TrimSpace(path) == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// LoadNixplayBridgeConfig reads YAML from path. A missing file returns defaults and no error.
func LoadNixplayBridgeConfig(path string) (NixplayBridgeConfig, error) {
	cfg := defaultNixplayBridgeConfig()
	if strings.TrimSpace(path) == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

func applyInkJoyEnvOverrides(cfg *InkJoyBridgeConfig) {
	if cfg.UpstreamUsr == "" {
		cfg.UpstreamUsr = os.Getenv("INKJOY_MQTT_USER")
	}
	if cfg.UpstreamPwd == "" {
		cfg.UpstreamPwd = os.Getenv("INKJOY_MQTT_PASSWORD")
	}
}

// reconcileHubDataDir picks the directory the hub uses for frame content caches.
// hub_data_dir must match hub config.yaml data_dir exactly on case-sensitive
// volumes (PhotoFrame vs photoframe are different paths).
func reconcileHubDataDir(configuredHubDataDir string) string {
	hubCfgPath, err := DefaultConfigPath()
	if err != nil {
		return strings.TrimSpace(configuredHubDataDir)
	}
	hubCfg, err := LoadHubConfig(hubCfgPath)
	if err != nil {
		return strings.TrimSpace(configuredHubDataDir)
	}
	bridgeHub := strings.TrimSpace(configuredHubDataDir)
	hubData, changed := reconcileHubDataDirWithHubConfig(hubCfg.DataDir, bridgeHub)
	if bridgeHub == "" && hubData != "" {
		log.Printf("bridge: hub_data_dir unset — using hub config data_dir %s", hubData)
	} else if changed {
		log.Printf("warn: bridge hub_data_dir %q != hub data_dir %q — writing frame cache to hub data_dir", bridgeHub, hubData)
	}
	return hubData
}

func reconcileHubDataDirWithHubConfig(hubConfigDataDir, inkjoyHubDataDir string) (string, bool) {
	hubData := strings.TrimSpace(hubConfigDataDir)
	bridgeHub := strings.TrimSpace(inkjoyHubDataDir)
	if hubData == "" {
		return bridgeHub, false
	}
	hubData = filepath.Clean(hubData)
	if bridgeHub == "" {
		return hubData, false
	}
	bridgeHub = filepath.Clean(bridgeHub)
	if dataDirsEquivalent(bridgeHub, hubData) {
		return bridgeHub, false
	}
	return hubData, true
}

func dataDirsEquivalent(a, b string) bool {
	if a == b {
		return true
	}
	ar, ae := resolveDataDir(a)
	br, be := resolveDataDir(b)
	if ae == nil && be == nil {
		return ar == br
	}
	return false
}

func resolveDataDir(path string) (string, error) {
	path = filepath.Clean(path)
	fi, err := os.Stat(path)
	if err != nil {
		return path, err
	}
	if !fi.IsDir() {
		return path, fmt.Errorf("not a directory")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs, nil
	}
	return real, nil
}

// HubConfig holds hub settings loaded from config.yaml.
type HubConfig struct {
	ListenMQTT      string `yaml:"listen_mqtt"`
	ListenHTTP      string `yaml:"listen_http"`
	Upstream        string `yaml:"upstream"`
	UpstreamUsr     string `yaml:"upstream_usr"`
	UpstreamPwd     string `yaml:"upstream_pwd"`
	UpstreamAllow   string `yaml:"upstream_allow"`   // frame→broker actions forwarded to cloud
	DownstreamAllow string `yaml:"downstream_allow"` // broker→frame actions forwarded to frame
	Intercept       string `yaml:"intercept"`        // broker→frame actions handled by hub, not forwarded
	DataDir         string `yaml:"data_dir"`
	ServerAddr      string `yaml:"server_addr"`
	DiscoverSubnets string `yaml:"discover_subnets"`
	LogDir          string `yaml:"log_dir"`
	CaptureDir      string `yaml:"capture_dir"` // empty/auto = {data_dir}/capture; off = disabled
	OTADir          string `yaml:"ota_dir"`     // empty/auto = {data_dir}/ota; off = disabled
}

// DefaultJoyousHubMQTTPort is the hub broker (bridges connect here).
const DefaultJoyousHubMQTTPort = 1883

// DefaultInkJoyFrameMQTTPort is where frames connect on inkjoy-bridge.
const DefaultInkJoyFrameMQTTPort = 11883

func defaultHubConfig() HubConfig {
	return HubConfig{
		ListenMQTT:      ":1883",
		ListenHTTP:      ":8080",
		UpstreamAllow:   inkjoybridge.DefaultUpstreamAllowCSV(),
		DownstreamAllow: inkjoybridge.DefaultDownstreamAllowCSV(),
		Intercept:       inkjoybridge.DefaultInterceptCSV(),
		DataDir:         "./data",
		DiscoverSubnets: "",
	}
}

// DefaultConfigPath returns the platform-specific config file location:
//   - macOS: ~/Library/Application Support/Joyous/config.yaml
//   - Linux: ~/.config/Joyous/config.yaml (or $XDG_CONFIG_HOME/Joyous/config.yaml)
func DefaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Joyous", "config.yaml"), nil
}

// LoadHubConfig reads YAML from path. A missing file returns defaults and no error.
func LoadHubConfig(path string) (HubConfig, error) {
	cfg := defaultHubConfig()
	if strings.TrimSpace(path) == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// applyEnvOverrides fills upstream credentials from INKJOY_MQTT_* when unset.
func applyEnvOverrides(cfg *HubConfig) {
	if cfg.UpstreamUsr == "" {
		cfg.UpstreamUsr = os.Getenv("INKJOY_MQTT_USER")
	}
	if cfg.UpstreamPwd == "" {
		cfg.UpstreamPwd = os.Getenv("INKJOY_MQTT_PASSWORD")
	}
}

// configPathFromArgs returns an explicit --config path before flag.Parse.
func configPathFromArgs(args []string) string {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--config" || arg == "-config" {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if strings.HasPrefix(arg, "--config=") {
			return strings.TrimPrefix(arg, "--config=")
		}
	}
	return ""
}

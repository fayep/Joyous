package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"joyous-hub/inkjoybridge"
)

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

func defaultHubConfig() HubConfig {
	return HubConfig{
		ListenMQTT:      ":11883",
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

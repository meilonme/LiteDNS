package config

import (
	"errors"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server ServerConfig `yaml:"server"`
	DB     DBConfig     `yaml:"db"`
	Auth   AuthConfig   `yaml:"auth"`
	DDNS   DDNSConfig   `yaml:"ddns"`
	Sync   SyncConfig   `yaml:"sync"`
	Logs   LogsConfig   `yaml:"logs"`
	IP     IPConfig     `yaml:"ip"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type DBConfig struct {
	Path string `yaml:"path"`
}

type AuthConfig struct {
	SessionTTLSeconds   int  `yaml:"session_ttl_sec"`
	ForceChangePassword bool `yaml:"force_change_password"`
}

type DDNSConfig struct {
	DefaultIntervalSec int   `yaml:"default_interval_sec"`
	RetryDelaysSec     []int `yaml:"retry_delays_sec"`
}

type SyncConfig struct {
	TTLSeconds int `yaml:"ttl_sec"`
}

type LogsConfig struct {
	RetentionDays int `yaml:"retention_days"`
}

type IPConfig struct {
	PublicIPCheck    bool     `yaml:"public_ip_check"`
	CheckIntervalSec int      `yaml:"check_interval_sec"`
	Sources          []string `yaml:"sources"`
}

func Load(path string) (Config, error) {
	cfg := defaultConfig()

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}

	applyEnvOverrides(&cfg)

	return cfg, nil
}

func defaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8080,
		},
		DB: DBConfig{
			Path: "data.db",
		},
		Auth: AuthConfig{
			SessionTTLSeconds:   43200,
			ForceChangePassword: true,
		},
		DDNS: DDNSConfig{
			DefaultIntervalSec: 300,
			RetryDelaysSec:     []int{2, 4, 8},
		},
		Sync: SyncConfig{
			TTLSeconds: 600,
		},
		Logs: LogsConfig{
			RetentionDays: 90,
		},
		IP: IPConfig{
			PublicIPCheck:    true,
			CheckIntervalSec: 300,
			Sources: []string{
				"https://api.ipify.org",
				"https://checkip.amazonaws.com",
				"https://ifconfig.co/ip",
			},
		},
	}
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("LITEDNS_SERVER_HOST"); v != "" {
		cfg.Server.Host = v
	}
	if v, ok := getenvInt("LITEDNS_SERVER_PORT"); ok {
		cfg.Server.Port = v
	}
	if v := os.Getenv("LITEDNS_DB_PATH"); v != "" {
		cfg.DB.Path = v
	}
	if v, ok := getenvInt("LITEDNS_SYNC_TTL_SEC"); ok {
		cfg.Sync.TTLSeconds = v
	}
	if v, ok := getenvInt("LITEDNS_DDNS_DEFAULT_INTERVAL_SEC"); ok {
		cfg.DDNS.DefaultIntervalSec = v
	}
	if v, ok := getenvInt("LITEDNS_AUTH_SESSION_TTL_SEC"); ok {
		cfg.Auth.SessionTTLSeconds = v
	}
	if v, ok := getenvInt("LITEDNS_LOGS_RETENTION_DAYS"); ok {
		cfg.Logs.RetentionDays = v
	}
	if v, ok := getenvBool("LITEDNS_IP_PUBLIC_CHECK"); ok {
		cfg.IP.PublicIPCheck = v
	}
	if v, ok := getenvInt("LITEDNS_IP_CHECK_INTERVAL_SEC"); ok {
		cfg.IP.CheckIntervalSec = v
	}
}

func getenvInt(key string) (int, bool) {
	raw := os.Getenv(key)
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return v, true
}

func getenvBool(key string) (bool, bool) {
	raw := os.Getenv(key)
	if raw == "" {
		return false, false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false
	}
	return v, true
}

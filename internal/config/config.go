package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	ListenAddr    string `yaml:"listen_addr"`
	WebListenAddr string `yaml:"web_listen_addr"`
	AdminKey      string `yaml:"admin_key"`
}

type StorageConfig struct {
	DataDir         string `yaml:"data_dir"`
	ModelRefreshSec int    `yaml:"model_refresh_sec"`
}

type ProviderConfig struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	BaseURL string   `yaml:"base_url"`
	APIKeys []string `yaml:"api_keys"`
	Enabled bool     `yaml:"enabled"`
}

type Config struct {
	Server    ServerConfig     `yaml:"server"`
	Storage   StorageConfig    `yaml:"storage"`
	Providers []ProviderConfig `yaml:"providers"`
}

func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			ListenAddr:    ":8080",
			WebListenAddr: ":8081",
			AdminKey:      "sk-admin-default",
		},
		Storage: StorageConfig{
			DataDir:         "./data",
			ModelRefreshSec: 3600,
		},
		Providers: []ProviderConfig{},
	}
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

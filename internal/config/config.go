package config

import (
	"fmt"
	"os"
	"regexp"

	"fleetdeck/internal/domain"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server ServerConfig   `yaml:"server"`
	Nodes  []domain.Node  `yaml:"nodes"`
}

type ServerConfig struct {
	Port         int    `yaml:"port"`
	Host         string `yaml:"host"`
	AuthUsername string `yaml:"auth_username"`
	AuthPassword string `yaml:"auth_password"`
	PollInterval int    `yaml:"poll_interval_seconds"`
}

var envVarRegex = regexp.MustCompile(`\$\{([a-zA-Z0-9_]+)\}`)

// LoadConfig reads a YAML file and substitutes environment variables (${VAR_NAME})
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	// Substitute ${ENV_VAR}
	expanded := envVarRegex.ReplaceAllStringFunc(string(data), func(match string) string {
		varName := match[2 : len(match)-1]
		if val, ok := os.LookupEnv(varName); ok {
			return val
		}
		return match
	})

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config yaml: %w", err)
	}

	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8089
	}
	if cfg.Server.PollInterval == 0 {
		cfg.Server.PollInterval = 300 // default 5 minutes
	}

	return &cfg, nil
}

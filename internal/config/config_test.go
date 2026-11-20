package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fleetdeck-cfg-*")
	if err != nil {
		t.Fatalf("temp dir err: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfgPath := filepath.Join(tmpDir, "fleet.yaml")
	cfgContent := `
server:
  host: "127.0.0.1"
  port: 9090
  poll_interval_seconds: 60

nodes:
  - id: "test-node"
    name: "Test Server"
    provider: "local_docker"
    tags: ["unit-test"]
    docker_access:
      driver: "local_socket"
      target: "/var/run/docker.sock"
`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("write cfg err: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if len(cfg.Nodes) != 1 || cfg.Nodes[0].ID != "test-node" {
		t.Errorf("unexpected nodes: %+v", cfg.Nodes)
	}
}

func TestLoadConfigEnvSubstitution(t *testing.T) {
	os.Setenv("TEST_FLEET_KEY", "secret_key_123")
	defer os.Unsetenv("TEST_FLEET_KEY")

	tmpDir, _ := os.MkdirTemp("", "fleetdeck-cfg-*")
	defer os.RemoveAll(tmpDir)

	cfgPath := filepath.Join(tmpDir, "fleet.yaml")
	cfgContent := `
server:
  host: "0.0.0.0"
  port: 8089

nodes:
  - id: "env-node"
    name: "Env Node"
    provider: "instabox"
    provider_config:
      api_key: "${TEST_FLEET_KEY}"
    docker_access:
      driver: "local_socket"
`
	_ = os.WriteFile(cfgPath, []byte(cfgContent), 0644)

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}

	if cfg.Nodes[0].ProviderConfig["api_key"] != "secret_key_123" {
		t.Errorf("expected secret_key_123, got %s", cfg.Nodes[0].ProviderConfig["api_key"])
	}
}

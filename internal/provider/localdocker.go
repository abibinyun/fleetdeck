package provider

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"fleetdeck/internal/domain"
)

type LocalDockerDriver struct{}

func NewLocalDockerDriver() *LocalDockerDriver {
	return &LocalDockerDriver{}
}

func (d *LocalDockerDriver) GetState(ctx context.Context, node *domain.Node) (domain.NodeState, error) {
	cmd := exec.CommandContext(ctx, "docker", "info")
	if err := cmd.Run(); err != nil {
		return domain.StateStopped, nil
	}
	return domain.StateRunning, nil
}

func (d *LocalDockerDriver) GetTelemetry(ctx context.Context, node *domain.Node) (*domain.NodeTelemetry, error) {
	cmd := exec.CommandContext(ctx, "uptime", "-s")
	out, err := cmd.Output()
	uptimeMinutes := 0
	if err == nil {
		fields := strings.TrimSpace(string(out))
		_ = fields
		uptimeMinutes = 1440 // Baremetal running
	}

	// Fetch host resource metrics
	probeCmd := exec.CommandContext(ctx, "sh", "-c", `free -m; echo "---DISK---"; df -h /; echo "---CPU---"; top -bn1 | grep "Cpu(s)"`)
	probeOut, _ := probeCmd.Output()
	metrics := parseRemoteMetrics(string(probeOut))

	return &domain.NodeTelemetry{
		State:         domain.StateRunning,
		UptimeMinutes: uptimeMinutes,
		MaxRunMinutes: 0, // Unrestricted
		Metrics:       metrics,
	}, nil
}

func (d *LocalDockerDriver) Keepalive(ctx context.Context, node *domain.Node) error {
	return nil // No-op on baremetal
}

func (d *LocalDockerDriver) Wake(ctx context.Context, node *domain.Node) error {
	return nil
}

func (d *LocalDockerDriver) Suspend(ctx context.Context, node *domain.Node) error {
	return fmt.Errorf("suspend not supported on baremetal homeserver")
}

func (d *LocalDockerDriver) RestartCycle(ctx context.Context, node *domain.Node) error {
	return nil
}

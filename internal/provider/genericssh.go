package provider

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"fleetdeck/internal/domain"
)

type GenericSSHDriver struct{}

func NewGenericSSHDriver() *GenericSSHDriver {
	return &GenericSSHDriver{}
}

func (d *GenericSSHDriver) buildSSHCommand(ctx context.Context, node *domain.Node, remoteCmd string) *exec.Cmd {
	target := node.DockerAccess.Target
	port := node.DockerAccess.Port
	if port == 0 {
		port = 22
	}
	user := node.DockerAccess.User
	if user == "" {
		user = "root"
	}

	host := target
	if !strings.Contains(host, "@") {
		host = fmt.Sprintf("%s@%s", user, target)
	}

	args := []string{
		"-F", "/dev/null",
		"-i", "/root/.ssh/id_ed25519",
		"-p", strconv.Itoa(port),
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		host,
		remoteCmd,
	}
	return exec.CommandContext(ctx, "ssh", args...)
}

func (d *GenericSSHDriver) GetState(ctx context.Context, node *domain.Node) (domain.NodeState, error) {
	cmd := d.buildSSHCommand(ctx, node, "docker info >/dev/null 2>&1 && echo OK")
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "OK" {
		return domain.StateStopped, nil
	}
	return domain.StateRunning, nil
}

func (d *GenericSSHDriver) GetTelemetry(ctx context.Context, node *domain.Node) (*domain.NodeTelemetry, error) {
	// Query uptime, memory, disk, and cpu in one single remote round-trip
	probeCmd := `cat /proc/uptime 2>/dev/null; echo "---METRICS---"; free -m 2>/dev/null; echo "---DISK---"; df -h / 2>/dev/null; echo "---CPU---"; top -bn1 2>/dev/null | grep "Cpu(s)"`
	cmd := d.buildSSHCommand(ctx, node, probeCmd)
	out, err := cmd.Output()
	uptimeMinutes := 1440
	var metrics *domain.NodeMetrics

	if err == nil {
		raw := string(out)
		parts := strings.Split(raw, "---METRICS---")
		if len(parts) > 0 {
			fields := strings.Fields(parts[0])
			if len(fields) > 0 {
				if sec, err := strconv.ParseFloat(fields[0], 64); err == nil {
					uptimeMinutes = int(sec / 60)
				}
			}
		}

		if len(parts) > 1 {
			metrics = parseRemoteMetrics(parts[1])
		}
	}

	state, _ := d.GetState(ctx, node)
	return &domain.NodeTelemetry{
		State:         state,
		UptimeMinutes: uptimeMinutes,
		MaxRunMinutes: 0, // Unrestricted on generic VPS
		Metrics:       metrics,
	}, nil
}

func parseRemoteMetrics(block string) *domain.NodeMetrics {
	m := &domain.NodeMetrics{
		CPUPerc:  "0%",
		MemPerc:  "0%",
		DiskPerc: "0%",
	}

	sections := strings.Split(block, "---DISK---")
	if len(sections) > 0 {
		// Parse free -m
		lines := strings.Split(sections[0], "\n")
		for _, l := range lines {
			if strings.HasPrefix(l, "Mem:") {
				f := strings.Fields(l)
				if len(f) >= 4 {
					tot, _ := strconv.Atoi(f[1])
					used, _ := strconv.Atoi(f[2])
					free, _ := strconv.Atoi(f[3])
					m.TotalMemMB = tot
					m.UsedMemMB = used
					m.FreeMemMB = free
					if tot > 0 {
						m.MemPerc = fmt.Sprintf("%.1f%%", float64(used)/float64(tot)*100)
					}
				}
			}
		}
	}

	if len(sections) > 1 {
		diskAndCpu := strings.Split(sections[1], "---CPU---")
		// Parse df -h /
		dfLines := strings.Split(diskAndCpu[0], "\n")
		for _, l := range dfLines {
			f := strings.Fields(l)
			if len(f) >= 6 && (strings.HasPrefix(f[0], "/dev") || strings.HasPrefix(f[0], "overlay") || f[5] == "/") {
				m.DiskTotalGB = f[1]
				m.DiskUsedGB = f[2]
				m.DiskFreeGB = f[3]
				m.DiskPerc = f[4]
				break
			}
		}

		// Parse CPU
		if len(diskAndCpu) > 1 {
			cpuLine := strings.TrimSpace(diskAndCpu[1])
			if strings.Contains(cpuLine, "id") {
				// e.g. %Cpu(s):  0.0 us,  2.3 sy,  0.0 ni, 97.7 id
				fields := strings.Split(cpuLine, ",")
				for _, part := range fields {
					if strings.Contains(part, "id") {
						valStr := strings.TrimSpace(strings.ReplaceAll(part, "id", ""))
						if idle, err := strconv.ParseFloat(valStr, 64); err == nil {
							used := 100.0 - idle
							if used < 0 {
								used = 0
							}
							m.CPUPerc = fmt.Sprintf("%.1f%%", used)
						}
					}
				}
			}
		}
	}

	return m
}

func (d *GenericSSHDriver) Keepalive(ctx context.Context, node *domain.Node) error {
	return nil // Dedicated VPS does not need keepalive renewal
}

func (d *GenericSSHDriver) Wake(ctx context.Context, node *domain.Node) error {
	return nil
}

func (d *GenericSSHDriver) Suspend(ctx context.Context, node *domain.Node) error {
	return fmt.Errorf("suspend not supported on generic SSH VPS")
}

func (d *GenericSSHDriver) RestartCycle(ctx context.Context, node *domain.Node) error {
	return nil
}

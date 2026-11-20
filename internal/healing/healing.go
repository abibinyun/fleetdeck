package healing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fleetdeck/internal/domain"
)

// Incident represents a diagnosed container crash or anomaly
type Incident struct {
	ID            string    `json:"id"`
	NodeID        string    `json:"node_id"`
	ContainerName string    `json:"container_name"`
	ExitCode      int       `json:"exit_code"`
	FailedAt      time.Time `json:"failed_at"`
	LogsTail      string    `json:"logs_tail"`
	RootCause     string    `json:"root_cause"`
	SuggestedFix  string    `json:"suggested_fix"`
	AutoApplied   bool      `json:"auto_applied"`
}

// Diagnose evaluates container log errors and gives actionable suggestions
func Diagnose(containerName string, logs string) (rootCause string, suggestedAction string) {
	lowerLogs := strings.ToLower(logs)

	switch {
	case strings.Contains(lowerLogs, "oomkilled") || strings.Contains(lowerLogs, "out of memory"):
		return "Out of Memory (OOM): Container exceeded allocated RAM threshold.",
			"Increase memory limit or optimize memory footprint."

	case strings.Contains(lowerLogs, "port already in use") || strings.Contains(lowerLogs, "address already in use"):
		return "Port Conflict: Desired host port is occupied by another process.",
			"Change exposed container port mapping."

	case strings.Contains(lowerLogs, "connection refused") || strings.Contains(lowerLogs, "dial tcp"):
		return "Upstream Network Failure: Dependent database or service unreachable.",
			"Verify upstream database hostname/credentials and ensure network attachment."

	case strings.Contains(lowerLogs, "permission denied") || strings.Contains(lowerLogs, "eacces"):
		return "Filesystem Permission Error: Container process lacks read/write access to mounted volume.",
			"Check volume folder permissions (chmod / chown) on host node."

	case strings.Contains(lowerLogs, "no such file or directory"):
		return "Missing File/Configuration: Entrypoint script or env file not found.",
			"Check container entrypoint and mounted config paths."

	default:
		return "Application runtime panic / abnormal termination.",
			"Inspect recent code commit changes and review environment variables."
	}
}

// SelfHealingEngine monitors unhealthy containers and suggests or applies auto-restart fixes
type SelfHealingEngine struct {
	docker domain.DockerCommander
}

func NewSelfHealingEngine(docker domain.DockerCommander) *SelfHealingEngine {
	return &SelfHealingEngine{docker: docker}
}

// AnalyzeContainer inspects a container's recent tail logs and produces an Incident report
func (e *SelfHealingEngine) AnalyzeContainer(ctx context.Context, node *domain.Node, c domain.Container) (*Incident, error) {
	logChan, err := e.docker.StreamLogs(ctx, node, c.Name, 40)
	if err != nil {
		return nil, fmt.Errorf("read logs: %w", err)
	}

	var sb strings.Builder
	for line := range logChan {
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	logs := sb.String()
	cause, fix := Diagnose(c.Name, logs)

	incident := &Incident{
		ID:            fmt.Sprintf("inc-%d", time.Now().UnixNano()),
		NodeID:        node.ID,
		ContainerName: c.Name,
		FailedAt:      time.Now(),
		LogsTail:      logs,
		RootCause:     cause,
		SuggestedFix:  fix,
		AutoApplied:   false,
	}

	return incident, nil
}

package docker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fleetdeck/internal/domain"
)

// AgentlessCommander manages docker containers across nodes without installing custom agents.
type AgentlessCommander struct{}

func NewAgentlessCommander() *AgentlessCommander {
	return &AgentlessCommander{}
}

// rawContainer represents docker inspect output format
type rawDockerInspect struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	Image  string `json:"Image"`
	Status string `json:"Status"`
	State  string `json:"State"`
	Ports  string `json:"Ports"`
	Labels string `json:"Labels"`
}

func (c *AgentlessCommander) executeCommand(ctx context.Context, node *domain.Node, args []string) ([]byte, error) {
	switch node.DockerAccess.Driver {
	case "local_socket", "":
		cmd := exec.CommandContext(ctx, "docker", args...)
		return cmd.CombinedOutput()

	case "instabox_exec":
		mid := node.ProviderConfig["machine_id"]
		key := node.ProviderConfig["api_key"]
		if mid == "" || key == "" {
			return nil, fmt.Errorf("missing machine_id or api_key for instabox_exec")
		}

		execArgs := []string{"exec", mid, "--timeout", "30", "--", "docker"}
		execArgs = append(execArgs, args...)
		cmd := exec.CommandContext(ctx, "instabox", execArgs...)
		cmd.Env = append(cmd.Environ(), fmt.Sprintf("INSTABOX_API_KEY=%s", key))
		return cmd.CombinedOutput()

	case "ssh":
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

		// Build quoted args for remote shell execution
		quoted := make([]string, len(args))
		for i, a := range args {
			quoted[i] = fmt.Sprintf("'%s'", strings.ReplaceAll(a, "'", "'\\''"))
		}
		dockerCmd := fmt.Sprintf("docker %s", strings.Join(quoted, " "))
		sshArgs := []string{
			"-q",
			"-F", "/dev/null",
			"-i", "/root/.ssh/id_ed25519",
			"-p", strconv.Itoa(port),
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=5",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			host,
			dockerCmd,
		}
		cmd := exec.CommandContext(ctx, "ssh", sshArgs...)
		return cmd.CombinedOutput()

	default:
		return nil, fmt.Errorf("unsupported docker driver: %s", node.DockerAccess.Driver)
	}
}

func (c *AgentlessCommander) ListContainers(ctx context.Context, node *domain.Node) ([]domain.Container, error) {
	// Robust tab-separated format avoiding JSON escape bugs with complex regex labels
	format := `{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.State}}\t{{.Ports}}\t{{.Label "com.docker.compose.project"}}`
	output, err := c.executeCommand(ctx, node, []string{"ps", "-a", "--format", format})
	if err != nil {
		return nil, fmt.Errorf("docker ps: %s (%w)", string(output), err)
	}

	var containers []domain.Container
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 6 {
			continue
		}

		id := parts[0]
		name := parts[1]
		image := parts[2]
		status := parts[3]
		state := parts[4]
		ports := parts[5]
		project := "standalone"
		if len(parts) >= 7 && parts[6] != "" {
			project = parts[6]
		}

		if project == "standalone" {
			nameParts := strings.Split(name, "-")
			if len(nameParts) > 1 {
				project = nameParts[0]
			}
		}

		containers = append(containers, domain.Container{
			ID:        id,
			Name:      name,
			Image:     image,
			State:     state,
			Status:    status,
			Project:   project,
			Ports:     ports,
			CreatedAt: time.Now(),
		})
	}

	if len(containers) > 0 {
		c.enrichWithStats(ctx, node, containers)
	}

	return containers, nil
}

func (c *AgentlessCommander) enrichWithStats(ctx context.Context, node *domain.Node, containers []domain.Container) {
	statsFormat := `{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}|{{.MemPerc}}`
	out, err := c.executeCommand(ctx, node, []string{"stats", "--no-stream", "--format", statsFormat})
	if err != nil {
		return
	}

	statsMap := make(map[string][]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, "|")
		if len(parts) == 4 {
			statsMap[strings.TrimSpace(parts[0])] = []string{
				strings.TrimSpace(parts[1]),
				strings.TrimSpace(parts[2]),
				strings.TrimSpace(parts[3]),
			}
		}
	}

	for i := range containers {
		if st, ok := statsMap[containers[i].Name]; ok {
			containers[i].CPUPerc = st[0]
			containers[i].MemUsage = st[1]
			containers[i].MemPerc = st[2]
		}
	}
}

func (c *AgentlessCommander) RestartContainer(ctx context.Context, node *domain.Node, containerName string) error {
	output, err := c.executeCommand(ctx, node, []string{"restart", containerName})
	if err != nil {
		return fmt.Errorf("docker restart: %s (%w)", string(output), err)
	}
	return nil
}

func (c *AgentlessCommander) StartContainer(ctx context.Context, node *domain.Node, containerName string) error {
	output, err := c.executeCommand(ctx, node, []string{"start", containerName})
	if err != nil {
		return fmt.Errorf("docker start: %s (%w)", string(output), err)
	}
	return nil
}

func (c *AgentlessCommander) StopContainer(ctx context.Context, node *domain.Node, containerName string) error {
	output, err := c.executeCommand(ctx, node, []string{"stop", containerName})
	if err != nil {
		return fmt.Errorf("docker stop: %s (%w)", string(output), err)
	}
	return nil
}

func (c *AgentlessCommander) StreamLogs(ctx context.Context, node *domain.Node, containerName string, tailLines int) (<-chan string, error) {
	ch := make(chan string, 100)
	go func() {
		defer close(ch)
		output, err := c.executeCommand(ctx, node, []string{"logs", "--tail", fmt.Sprintf("%d", tailLines), containerName})
		if err != nil {
			ch <- fmt.Sprintf("Error fetching logs: %v (%s)", err, string(output))
			return
		}
		for _, line := range strings.Split(string(output), "\n") {
			ch <- line
		}
	}()
	return ch, nil
}

// DeployApp spins up an application container on the chosen target node
func (c *AgentlessCommander) DeployApp(ctx context.Context, node *domain.Node, app *domain.Application) error {
	// 1. If container with same name exists, stop and remove it
	_ = c.StopContainer(ctx, node, app.Name)
	_, _ = c.executeCommand(ctx, node, []string{"rm", "-f", app.Name})

	// 2. Build docker run arguments
	args := []string{"run", "-d", "--name", app.Name, "--restart", "unless-stopped"}

	// Network
	args = append(args, "--network", "gateway")

	// Ingress Traefik Labels (Vercel-like automated routing)
	if app.Domain != "" {
		routerName := strings.ReplaceAll(app.Name, ".", "-")
		args = append(args,
			"--label", "traefik.enable=true",
			"--label", fmt.Sprintf("traefik.http.routers.%s.rule=Host(`%s`)", routerName, app.Domain),
			"--label", fmt.Sprintf("traefik.http.routers.%s.entrypoints=web", routerName),
		)
		if app.Port > 0 {
			args = append(args,
				"--label", fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port=%d", routerName, app.Port),
			)
		}
	}

	// Environment Variables
	for k, v := range app.EnvVars {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	// Metadata label
	args = append(args,
		"--label", fmt.Sprintf("fleetdeck.app.id=%s", app.ID),
		"--label", fmt.Sprintf("com.docker.compose.project=%s", app.Name),
	)

	// Image or Static Nginx
	image := app.Image
	switch app.Type {
	case domain.AppTypeStaticNginx:
		// Create temporary html on host if local, or run static alpine nginx
		image = "nginx:alpine"
		if app.StaticHTML != "" && (node.DockerAccess.Driver == "local_socket" || node.DockerAccess.Driver == "") {
			tmpDir := filepath.Join(os.TempDir(), "fleetdeck-static", app.Name)
			_ = os.MkdirAll(tmpDir, 0755)
			_ = os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte(app.StaticHTML), 0644)
			args = append(args, "-v", fmt.Sprintf("%s:/usr/share/nginx/html:ro", tmpDir))
		}
	case domain.AppTypeTemplate, domain.AppTypeDockerImage:
		if image == "" {
			image = "nginx:alpine"
		}
	default:
		if image == "" {
			image = "nginx:alpine"
		}
	}

	args = append(args, image)

	out, err := c.executeCommand(ctx, node, args)
	if err != nil {
		return fmt.Errorf("deploy app container failed: %s (%w)", string(out), err)
	}

	return nil
}

// DeleteApp stops and removes application container
func (c *AgentlessCommander) DeleteApp(ctx context.Context, node *domain.Node, app *domain.Application) error {
	out, err := c.executeCommand(ctx, node, []string{"rm", "-f", app.Name})
	if err != nil {
		return fmt.Errorf("delete app failed: %s (%w)", string(out), err)
	}
	return nil
}

package keeper

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"fleetdeck/internal/builder"
	"fleetdeck/internal/domain"
	"fleetdeck/internal/store"
)

type Engine struct {
	nodes           []*domain.Node
	drivers         map[domain.NodeProvider]domain.NodeDriver
	dockerCommander domain.DockerCommander
	appStore        *store.AppStore
	gitBuilder      *builder.GitBuilder
	mu              sync.RWMutex
	pollInterval    time.Duration
	stopChan        chan struct{}
}

func NewEngine(nodes []domain.Node, drivers map[domain.NodeProvider]domain.NodeDriver, docker domain.DockerCommander, appStore *store.AppStore, gitBuilder *builder.GitBuilder, pollInterval time.Duration) *Engine {
	nodePtrs := make([]*domain.Node, 0)
	for i := range nodes {
		cp := nodes[i]
		nodePtrs = append(nodePtrs, &cp)
	}

	// Merge custom nodes saved via UI
	if appStore != nil {
		for _, cn := range appStore.ListCustomNodes() {
			nodePtrs = append(nodePtrs, cn)
		}
	}

	return &Engine{
		nodes:           nodePtrs,
		drivers:         drivers,
		dockerCommander: docker,
		appStore:        appStore,
		gitBuilder:      gitBuilder,
		pollInterval:    pollInterval,
		stopChan:        make(chan struct{}),
	}
}

func (e *Engine) Start(ctx context.Context) {
	log.Printf("[FleetDeck Keeper] Engine started with %d nodes (interval: %v)", len(e.nodes), e.pollInterval)
	e.ReconcileAll(ctx)

	ticker := time.NewTicker(e.pollInterval)
	go func() {
		for {
			select {
			case <-ticker.C:
				e.ReconcileAll(ctx)
			case <-e.stopChan:
				ticker.Stop()
				return
			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()
}

func (e *Engine) Stop() {
	close(e.stopChan)
}

func (e *Engine) GetNodes() []domain.Node {
	e.mu.RLock()
	defer e.mu.RUnlock()
	res := make([]domain.Node, len(e.nodes))
	for i, n := range e.nodes {
		res[i] = *n
	}
	return res
}

func (e *Engine) FindNode(id string) *domain.Node {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, n := range e.nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

func (e *Engine) GetAppStore() *store.AppStore {
	return e.appStore
}

// AddNode dynamically registers a new server from UI and persists it
func (e *Engine) AddNode(ctx context.Context, node *domain.Node) error {
	e.mu.Lock()
	for _, n := range e.nodes {
		if n.ID == node.ID {
			e.mu.Unlock()
			return fmt.Errorf("node with ID '%s' already exists", node.ID)
		}
	}
	e.nodes = append(e.nodes, node)
	e.mu.Unlock()

	if e.appStore != nil {
		_ = e.appStore.SaveCustomNode(node)
	}

	go e.reconcileNode(context.Background(), node)
	return nil
}

// BootstrapRemoteNode tests SSH connection and auto-installs Docker if missing
func (e *Engine) BootstrapRemoteNode(ctx context.Context, host string, port int, user string) (string, error) {
	if port == 0 {
		port = 22
	}
	if user == "" {
		user = "root"
	}
	target := fmt.Sprintf("%s@%s", user, host)

	// 1. Check SSH connectivity
	checkSSH := exec.CommandContext(ctx, "ssh",
		"-F", "/dev/null",
		"-i", "/root/.ssh/id_ed25519",
		"-p", strconv.Itoa(port),
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		target,
		"echo SSH_OK",
	)
	outSSH, err := checkSSH.CombinedOutput()
	if err != nil || !strings.Contains(string(outSSH), "SSH_OK") {
		return "", fmt.Errorf("SSH connection failed. Ensure FleetDeck public key is added to target VPS: %s (%w)", string(outSSH), err)
	}

	// 2. Check if Docker is installed
	checkDocker := exec.CommandContext(ctx, "ssh",
		"-F", "/dev/null",
		"-i", "/root/.ssh/id_ed25519",
		"-p", strconv.Itoa(port),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		target,
		"which docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 && echo DOCKER_OK || echo NO_DOCKER",
	)
	outDocker, _ := checkDocker.CombinedOutput()

	if strings.Contains(string(outDocker), "DOCKER_OK") {
		return "Node connected successfully. Docker is already active.", nil
	}

	// 3. Auto-install Docker
	log.Printf("[FleetDeck Bootstrap] Installing Docker automatically on %s...", target)
	installDocker := exec.CommandContext(ctx, "ssh",
		"-F", "/dev/null",
		"-i", "/root/.ssh/id_ed25519",
		"-p", strconv.Itoa(port),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		target,
		"curl -fsSL https://get.docker.com | sh && systemctl enable --now docker",
	)
	outInstall, err := installDocker.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("automated Docker installation failed: %s (%w)", string(outInstall), err)
	}

	return "Docker installed and started automatically on remote VPS.", nil
}

func (e *Engine) ReconcileAll(ctx context.Context) {
	var wg sync.WaitGroup
	for _, node := range e.nodes {
		wg.Add(1)
		go func(n *domain.Node) {
			defer wg.Done()
			e.reconcileNode(ctx, n)
		}(node)
	}
	wg.Wait()
}

func (e *Engine) reconcileNode(ctx context.Context, node *domain.Node) {
	driver, ok := e.drivers[node.Provider]
	if !ok {
		return
	}

	telemetry, err := driver.GetTelemetry(ctx, node)
	e.mu.Lock()
	if err != nil {
		node.ErrorMessage = err.Error()
		node.State = domain.StateUnknown
		e.mu.Unlock()
		return
	}

	node.State = telemetry.State
	node.UptimeMinutes = telemetry.UptimeMinutes
	node.MaxRunMinutes = telemetry.MaxRunMinutes
	node.CooldownUntil = telemetry.CooldownUntil
	if telemetry.Metrics != nil {
		node.Metrics = telemetry.Metrics
	}
	node.ErrorMessage = ""
	e.mu.Unlock()

	if node.Provider == domain.ProviderInstaBox {
		e.handleInstaBoxLifecycle(ctx, node, driver)
	}

	if node.State == domain.StateRunning {
		containers, err := e.dockerCommander.ListContainers(ctx, node)
		e.mu.Lock()
		if err == nil {
			node.Containers = containers
		}
		e.mu.Unlock()
	}
}

func (e *Engine) handleInstaBoxLifecycle(ctx context.Context, node *domain.Node, driver domain.NodeDriver) {
	if node.State == domain.StateSuspended {
		if err := driver.Wake(ctx, node); err == nil {
			node.State = domain.StateRunning
		} else {
			return
		}
	}

	if node.State == domain.StateCooldown {
		return
	}

	threshold := 110
	if thVal, ok := node.ProviderConfig["reset_threshold_minutes"]; ok {
		if val, err := strconv.Atoi(thVal); err == nil {
			threshold = val
		}
	}

	if node.UptimeMinutes >= threshold {
		_ = driver.RestartCycle(ctx, node)
		return
	}

	if node.State == domain.StateRunning {
		if err := driver.Keepalive(ctx, node); err == nil {
			now := time.Now()
			e.mu.Lock()
			node.LastKeepalive = &now
			e.mu.Unlock()
		}
	}
}

func (e *Engine) TriggerAction(ctx context.Context, nodeID, action, target string) error {
	node := e.FindNode(nodeID)
	if node == nil {
		return fmt.Errorf("node not found: %s", nodeID)
	}

	driver, ok := e.drivers[node.Provider]
	if !ok {
		return fmt.Errorf("no driver for provider: %s", node.Provider)
	}

	switch action {
	case "wake":
		return driver.Wake(ctx, node)
	case "suspend":
		return driver.Suspend(ctx, node)
	case "cycle":
		return driver.RestartCycle(ctx, node)
	case "docker_restart":
		return e.dockerCommander.RestartContainer(ctx, node, target)
	case "docker_start":
		return e.dockerCommander.StartContainer(ctx, node, target)
	case "docker_stop":
		return e.dockerCommander.StopContainer(ctx, node, target)
	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

// DeployApplication deploys an application onto its designated node (handles Git builds too)
func (e *Engine) DeployApplication(ctx context.Context, app *domain.Application) error {
	node := e.FindNode(app.NodeID)
	if node == nil {
		return fmt.Errorf("target node not found: %s", app.NodeID)
	}

	app.Status = domain.AppStatusBuilding
	_ = e.appStore.Save(app)

	// If GitRepo type, clone and build image first
	if app.Type == domain.AppTypeGitRepo {
		log.Printf("[FleetDeck Builder] Building application '%s' from git repo %s (%s)...", app.Name, app.GitRepo, app.GitBranch)
		imageTag, err := e.gitBuilder.BuildFromGit(ctx, app)
		if err != nil {
			app.Status = domain.AppStatusFailed
			app.DeployLog = fmt.Sprintf("Git build failed at %s: %v", time.Now().Format(time.RFC3339), err)
			_ = e.appStore.Save(app)
			return err
		}
		app.Image = imageTag
	}

	err := e.dockerCommander.DeployApp(ctx, node, app)
	now := time.Now()
	app.LastDeploy = &now

	if err != nil {
		app.Status = domain.AppStatusFailed
		app.DeployLog = fmt.Sprintf("Deployment failed at %s: %v", now.Format(time.RFC3339), err)
		_ = e.appStore.Save(app)
		return err
	}

	app.Status = domain.AppStatusRunning
	app.DeployLog = fmt.Sprintf("Successfully deployed to node %s at %s", node.Name, now.Format(time.RFC3339))
	_ = e.appStore.Save(app)

	go e.reconcileNode(context.Background(), node)
	return nil
}

// DeleteApplication removes container and deletes from store
func (e *Engine) DeleteApplication(ctx context.Context, appID string) error {
	app := e.appStore.Get(appID)
	if app == nil {
		return fmt.Errorf("application not found: %s", appID)
	}

	if node := e.FindNode(app.NodeID); node != nil {
		_ = e.dockerCommander.DeleteApp(ctx, node, app)
		go e.reconcileNode(context.Background(), node)
	}

	return e.appStore.Delete(appID)
}

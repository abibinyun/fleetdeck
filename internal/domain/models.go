package domain

import (
	"context"
	"time"
)

// NodeProvider defines the provider type for an infrastructure node.
type NodeProvider string

const (
	ProviderLocalDocker NodeProvider = "local_docker"
	ProviderInstaBox    NodeProvider = "instabox"
	ProviderGenericSSH  NodeProvider = "generic_ssh"
)

// NodeState indicates the current lifecycle state of a compute node.
type NodeState string

const (
	StateRunning   NodeState = "running"
	StateSuspended NodeState = "suspended"
	StateStopped   NodeState = "stopped"
	StateCooldown  NodeState = "cooldown"
	StateUnknown   NodeState = "unknown"
)

// AppType defines what kind of application is deployed
type AppType string

const (
	AppTypeDockerImage   AppType = "docker_image"
	AppTypeDockerCompose AppType = "docker_compose"
	AppTypeStaticNginx   AppType = "static_nginx"
	AppTypeGitRepo       AppType = "git_repo"
	AppTypeTemplate      AppType = "template"
)

// AppStatus indicates the lifecycle of an application
type AppStatus string

const (
	AppStatusBuilding AppStatus = "building"
	AppStatusRunning  AppStatus = "running"
	AppStatusStopped  AppStatus = "stopped"
	AppStatusFailed   AppStatus = "failed"
)

// Application represents a deployable workload (like Vercel/Heroku app entity)
type Application struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        AppType           `json:"type"`
	NodeID      string            `json:"node_id"`
	Status      AppStatus         `json:"status"`
	Domain      string            `json:"domain,omitempty"`
	Port        int               `json:"port,omitempty"`
	Image       string            `json:"image,omitempty"`
	GitRepo     string            `json:"git_repo,omitempty"`
	GitBranch   string            `json:"git_branch,omitempty"`
	WebhookKey  string            `json:"webhook_key,omitempty"` // Secret token for github webhook verification
	ComposeYAML string            `json:"compose_yaml,omitempty"`
	StaticHTML  string            `json:"static_html,omitempty"`
	TemplateID  string            `json:"template_id,omitempty"`
	EnvVars     map[string]string `json:"env_vars,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	LastDeploy  *time.Time        `json:"last_deploy,omitempty"`
	DeployLog   string            `json:"deploy_log,omitempty"`
}

// SystemSettings holds onboarding and global PaaS configurations
type SystemSettings struct {
	IsInitialized bool   `json:"is_initialized"`
	AdminUsername string `json:"admin_username"`
	AdminPassword string `json:"admin_password"`
	RootDomain    string `json:"root_domain"`    // e.g. "example.com"
	GitWebhookURL string `json:"git_webhook_url"` // public webhook endpoint
}

// Template represents a 1-click installable open-source stack
type Template struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Category    string            `json:"category"`
	Description string            `json:"description"`
	Icon        string            `json:"icon"`
	Image       string            `json:"image"`
	DefaultPort int               `json:"default_port"`
	DefaultEnv  map[string]string `json:"default_env"`
	ComposeYAML string            `json:"compose_yaml,omitempty"`
}

// NodeMetrics represents real-time hardware usage of the compute node
type NodeMetrics struct {
	TotalMemMB int    `json:"total_mem_mb"`
	UsedMemMB  int    `json:"used_mem_mb"`
	FreeMemMB  int    `json:"free_mem_mb"`
	MemPerc    string `json:"mem_perc"`

	DiskTotalGB string `json:"disk_total_gb"`
	DiskUsedGB  string `json:"disk_used_gb"`
	DiskFreeGB  string `json:"disk_free_gb"`
	DiskPerc    string `json:"disk_perc"`

	CPUPerc string `json:"cpu_perc"`
}

// Node represents an infrastructure compute host.
type Node struct {
	ID             string             `json:"id" yaml:"id"`
	Name           string             `json:"name" yaml:"name"`
	Provider       NodeProvider       `json:"provider" yaml:"provider"`
	Tags           []string           `json:"tags" yaml:"tags"`
	IngressDomain  string             `json:"ingress_domain,omitempty" yaml:"ingress_domain,omitempty"`
	DirectURL      string             `json:"direct_url,omitempty" yaml:"direct_url,omitempty"`
	ProviderConfig map[string]string  `json:"provider_config,omitempty" yaml:"provider_config,omitempty"`
	DockerAccess   DockerAccessConfig `json:"docker_access" yaml:"docker_access"`

	// Dynamic Telemetry (hydrated at runtime)
	State         NodeState    `json:"state"`
	UptimeMinutes int          `json:"uptime_minutes"`
	MaxRunMinutes int          `json:"max_run_minutes"`
	CooldownUntil *time.Time   `json:"cooldown_until,omitempty"`
	LastKeepalive *time.Time   `json:"last_keepalive,omitempty"`
	Containers    []Container  `json:"containers,omitempty"`
	Metrics       *NodeMetrics `json:"metrics,omitempty"`
	ErrorMessage  string       `json:"error_message,omitempty"`
}

// DockerAccessConfig defines how FleetDeck interacts with the host's Docker daemon agentlessly.
type DockerAccessConfig struct {
	Driver string `json:"driver" yaml:"driver"` // "local_socket", "instabox_exec", "ssh"
	Target string `json:"target,omitempty" yaml:"target,omitempty"`
	Port   int    `json:"port,omitempty" yaml:"port,omitempty"`
	User   string `json:"user,omitempty" yaml:"user,omitempty"`
}

// Container represents a docker container running on a node.
type Container struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	State     string            `json:"state"`
	Status    string            `json:"status"`
	CPUPerc   string            `json:"cpu_perc,omitempty"`
	MemUsage  string            `json:"mem_usage,omitempty"`
	MemPerc   string            `json:"mem_perc,omitempty"`
	Project   string            `json:"project"`
	Ports     string            `json:"ports"`
	CreatedAt time.Time         `json:"created_at"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// NodeDriver abstracts infrastructure lifecycle operations for any provider.
type NodeDriver interface {
	GetState(ctx context.Context, node *Node) (NodeState, error)
	GetTelemetry(ctx context.Context, node *Node) (*NodeTelemetry, error)
	Keepalive(ctx context.Context, node *Node) error
	Wake(ctx context.Context, node *Node) error
	Suspend(ctx context.Context, node *Node) error
	RestartCycle(ctx context.Context, node *Node) error
}

// NodeTelemetry holds raw provider-specific metrics.
type NodeTelemetry struct {
	State         NodeState
	UptimeMinutes int
	MaxRunMinutes int
	CooldownUntil *time.Time
	Metrics       *NodeMetrics
	RawJSON       string
}

// DockerCommander executes docker commands on target nodes agentlessly.
type DockerCommander interface {
	ListContainers(ctx context.Context, node *Node) ([]Container, error)
	RestartContainer(ctx context.Context, node *Node, containerName string) error
	StartContainer(ctx context.Context, node *Node, containerName string) error
	StopContainer(ctx context.Context, node *Node, containerName string) error
	StreamLogs(ctx context.Context, node *Node, containerName string, tailLines int) (<-chan string, error)
	DeployApp(ctx context.Context, node *Node, app *Application) error
	DeleteApp(ctx context.Context, node *Node, app *Application) error
}

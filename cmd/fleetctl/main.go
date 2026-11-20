package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

type Config struct {
	Endpoint string
	Username string
	Password string
}

func loadClientConfig() Config {
	ep := os.Getenv("FLEETDECK_URL")
	if ep == "" {
		ep = "http://localhost:8089"
	}
	u := os.Getenv("FLEETDECK_USER")
	p := os.Getenv("FLEETDECK_PASS")
	return Config{
		Endpoint: strings.TrimRight(ep, "/"),
		Username: u,
		Password: p,
	}
}

func makeRequest(cfg Config, method, path string, body io.Reader) (*http.Response, error) {
	url := fmt.Sprintf("%s%s", cfg.Endpoint, path)
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if cfg.Username != "" && cfg.Password != "" {
		req.SetBasicAuth(cfg.Username, cfg.Password)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	return client.Do(req)
}

func printHelp() {
	help := `fleetctl - Instant CLI Companion for FleetDeck PaaS

Usage:
  fleetctl <command> [arguments]

Available Commands:
  status                   Display cluster nodes, health, and container stats
  apps                     List all deployed applications
  deploy                   Deploy an application or 1-click template to a node
  templates                List available 1-click templates (PocketBase, N8N, PostgreSQL, etc.)
  logs <app-id>            Stream or view logs for a deployed application
  version                  Show fleetctl version

Environment Variables:
  FLEETDECK_URL            FleetDeck endpoint (default: http://localhost:8089)
  FLEETDECK_USER           Basic auth username (optional)
  FLEETDECK_PASS           Basic auth password (optional)

Examples:
  fleetctl status
  fleetctl apps
  fleetctl deploy --name my-pocketbase --node node-vps --template pocketbase
  fleetctl deploy --name my-api --node node-vps --image golang:1.22-alpine --port 8080
`
	fmt.Print(help)
}

func main() {
	if len(os.Args) < 2 {
		printHelp()
		os.Exit(0)
	}

	cfg := loadClientConfig()
	cmd := os.Args[1]

	switch cmd {
	case "status":
		handleStatus(cfg)
	case "apps":
		handleApps(cfg)
	case "deploy":
		handleDeploy(cfg, os.Args[2:])
	case "templates":
		handleTemplates(cfg)
	case "version":
		fmt.Println("fleetctl v1.1.0-alpha (Go native)")
	case "help", "--help", "-h":
		printHelp()
	default:
		fmt.Printf("Unknown command: %s\nRun 'fleetctl help' for usage.\n", cmd)
		os.Exit(1)
	}
}

func handleStatus(cfg Config) {
	resp, err := makeRequest(cfg, "GET", "/api/nodes", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to FleetDeck (%s): %v\n", cfg.Endpoint, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "API returned status %d\n", resp.StatusCode)
		os.Exit(1)
	}

	var nodes []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Provider   string `json:"provider"`
		State      string `json:"state"`
		Containers []struct {
			Name     string `json:"name"`
			Image    string `json:"image"`
			Status   string `json:"status"`
			CPUPerc  string `json:"cpu_perc"`
			MemUsage string `json:"mem_usage"`
		} `json:"containers"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to decode response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("FleetDeck Control Plane: %s\n\n", cfg.Endpoint)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NODE ID\tNAME\tPROVIDER\tSTATE\tCONTAINERS")
	for _, n := range nodes {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d running\n", n.ID, n.Name, n.Provider, n.State, len(n.Containers))
	}
	w.Flush()

	fmt.Println("\nLive Workloads:")
	w2 := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w2, "NODE\tCONTAINER\tIMAGE\tSTATUS\tCPU\tMEM")
	for _, n := range nodes {
		for _, c := range n.Containers {
			cpu := c.CPUPerc
			if cpu == "" {
				cpu = "n/a"
			}
			mem := c.MemUsage
			if mem == "" {
				mem = "n/a"
			}
			fmt.Fprintf(w2, "%s\t%s\t%s\t%s\t%s\t%s\n", n.ID, c.Name, c.Image, c.Status, cpu, mem)
		}
	}
	w2.Flush()
}

func handleApps(cfg Config) {
	resp, err := makeRequest(cfg, "GET", "/api/apps", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var apps []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Type    string `json:"type"`
		NodeID  string `json:"node_id"`
		Status  string `json:"status"`
		Domain  string `json:"domain"`
		GitRepo string `json:"git_repo"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apps); err != nil {
		fmt.Fprintf(os.Stderr, "Decode error: %v\n", err)
		os.Exit(1)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "APP ID\tNAME\tTYPE\tNODE\tSTATUS\tDOMAIN")
	for _, a := range apps {
		dom := a.Domain
		if dom == "" {
			dom = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", a.ID, a.Name, a.Type, a.NodeID, a.Status, dom)
	}
	w.Flush()
}

func handleTemplates(cfg Config) {
	resp, err := makeRequest(cfg, "GET", "/api/templates", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var tpls []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Category    string `json:"category"`
		Description string `json:"description"`
		Image       string `json:"image"`
		DefaultPort int    `json:"default_port"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tpls); err != nil {
		fmt.Fprintf(os.Stderr, "Decode error: %v\n", err)
		os.Exit(1)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "TEMPLATE ID\tNAME\tCATEGORY\tDEFAULT PORT\tDESCRIPTION")
	for _, t := range tpls {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", t.ID, t.Name, t.Category, t.DefaultPort, t.Description)
	}
	w.Flush()
}

func handleDeploy(cfg Config, args []string) {
	fs := flag.NewFlagSet("deploy", flag.ExitOnError)
	name := fs.String("name", "", "Application name (slug, e.g. my-app)")
	nodeID := fs.String("node", "", "Target node ID (e.g. node-local, vps-nat)")
	templateID := fs.String("template", "", "1-Click Template ID (optional)")
	image := fs.String("image", "", "Docker Image (optional)")
	domain := fs.String("domain", "", "Custom domain or magic sslip.io will be used")
	port := fs.Int("port", 80, "Container target port")
	gitRepo := fs.String("git", "", "Git Repository URL (optional)")
	gitBranch := fs.String("branch", "main", "Git branch")

	_ = fs.Parse(args)

	if *name == "" || *nodeID == "" {
		fmt.Println("Error: --name and --node are required.")
		fmt.Println("Example: fleetctl deploy --name my-pocketbase --node node-vps --template pocketbase")
		os.Exit(1)
	}

	appType := "docker_image"
	if *templateID != "" {
		appType = "template"
	} else if *gitRepo != "" {
		appType = "git_repo"
	}

	payload := map[string]interface{}{
		"name":        *name,
		"node_id":     *nodeID,
		"type":        appType,
		"template_id": *templateID,
		"image":       *image,
		"domain":      *domain,
		"port":        *port,
		"git_repo":    *gitRepo,
		"git_branch":  *gitBranch,
	}

	data, _ := json.Marshal(payload)
	fmt.Printf("[fleetctl] Deploying '%s' to '%s'...\n", *name, *nodeID)
	resp, err := makeRequest(cfg, "POST", "/api/apps", bytes.NewReader(data))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Deployment failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		fmt.Fprintf(os.Stderr, "Deploy error (%d): %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var res struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Domain string `json:"domain"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(body, &res)

	fmt.Printf("✓ Application '%s' deployed successfully!\n", res.Name)
	if res.Domain != "" {
		fmt.Printf("  URL: http://%s\n", res.Domain)
	}
	fmt.Printf("  Status: %s\n", res.Status)
}

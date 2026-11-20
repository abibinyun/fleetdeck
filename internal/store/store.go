package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"fleetdeck/internal/domain"
)

// AppStore manages persistent application metadata, custom nodes, and system settings
type AppStore struct {
	mu           sync.RWMutex
	filePath     string
	settingsPath string
	nodesPath    string
	apps         map[string]*domain.Application
	customNodes  map[string]*domain.Node
	settings     *domain.SystemSettings
}

func NewAppStore(dataDir string) (*AppStore, error) {
	_ = os.MkdirAll(dataDir, 0755)
	storePath := filepath.Join(dataDir, "applications.json")
	settingsPath := filepath.Join(dataDir, "settings.json")
	nodesPath := filepath.Join(dataDir, "custom_nodes.json")

	s := &AppStore{
		filePath:     storePath,
		settingsPath: settingsPath,
		nodesPath:    nodesPath,
		apps:         make(map[string]*domain.Application),
		customNodes:  make(map[string]*domain.Node),
		settings: &domain.SystemSettings{
			IsInitialized: false,
			RootDomain:    "",
		},
	}

	// Load applications
	if data, err := os.ReadFile(storePath); err == nil {
		var list []*domain.Application
		if err := json.Unmarshal(data, &list); err == nil {
			for _, app := range list {
				s.apps[app.ID] = app
			}
		}
	}

	// Load settings
	if data, err := os.ReadFile(settingsPath); err == nil {
		var st domain.SystemSettings
		if err := json.Unmarshal(data, &st); err == nil {
			s.settings = &st
		}
	}

	// Load custom nodes created from UI
	if data, err := os.ReadFile(nodesPath); err == nil {
		var list []*domain.Node
		if err := json.Unmarshal(data, &list); err == nil {
			for _, n := range list {
				s.customNodes[n.ID] = n
			}
		}
	}

	return s, nil
}

func (s *AppStore) GetSettings() domain.SystemSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return *s.settings
}

func (s *AppStore) SaveSettings(st domain.SystemSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st.IsInitialized = true
	s.settings = &st

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.settingsPath, data, 0644)
}

func (s *AppStore) List() []*domain.Application {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]*domain.Application, 0, len(s.apps))
	for _, app := range s.apps {
		res = append(res, app)
	}
	return res
}

func (s *AppStore) Get(id string) *domain.Application {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.apps[id]
}

func (s *AppStore) Save(app *domain.Application) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if app.CreatedAt.IsZero() {
		app.CreatedAt = now
	}
	app.UpdatedAt = now
	s.apps[app.ID] = app

	return s.persistApps()
}

func (s *AppStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.apps, id)
	return s.persistApps()
}

func (s *AppStore) persistApps() error {
	list := make([]*domain.Application, 0, len(s.apps))
	for _, app := range s.apps {
		list = append(list, app)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal apps: %w", err)
	}
	return os.WriteFile(s.filePath, data, 0644)
}

// Custom Nodes Management (Created via UI)
func (s *AppStore) ListCustomNodes() []*domain.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]*domain.Node, 0, len(s.customNodes))
	for _, n := range s.customNodes {
		res = append(res, n)
	}
	return res
}

func (s *AppStore) SaveCustomNode(node *domain.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.customNodes[node.ID] = node
	return s.persistCustomNodes()
}

func (s *AppStore) DeleteCustomNode(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.customNodes, id)
	return s.persistCustomNodes()
}

func (s *AppStore) persistCustomNodes() error {
	list := make([]*domain.Node, 0, len(s.customNodes))
	for _, n := range s.customNodes {
		list = append(list, n)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal custom nodes: %w", err)
	}
	return os.WriteFile(s.nodesPath, data, 0644)
}

// Built-in 1-Click App Templates (Curated Open Source Stacks)
func GetDefaultTemplates() []domain.Template {
	return []domain.Template{
		{
			ID:          "nginx-static",
			Name:        "Static HTML / Nginx",
			Category:    "Web",
			Description: "High-performance Alpine Nginx server for static frontend preview.",
			Icon:        "globe",
			Image:       "nginx:alpine",
			DefaultPort: 80,
		},
		{
			ID:          "pocketbase",
			Name:        "PocketBase",
			Category:    "Backend",
			Description: "Open Source backend in 1 file with realtime SQLite database and auth.",
			Icon:        "server",
			Image:       "ghcr.io/muchobien/pocketbase:latest",
			DefaultPort: 8090,
		},
		{
			ID:          "postgresql",
			Name:        "PostgreSQL 16",
			Category:    "Database",
			Description: "Robust open-source relational SQL database with healthcheck.",
			Icon:        "database",
			Image:       "postgres:16-alpine",
			DefaultPort: 5432,
			DefaultEnv: map[string]string{
				"POSTGRES_DB":       "appdb",
				"POSTGRES_USER":     "admin",
				"POSTGRES_PASSWORD": "supersecretpassword",
			},
		},
		{
			ID:          "redis-cache",
			Name:        "Redis 7 Alpine",
			Category:    "Database",
			Description: "Ultra-fast in-memory key-value cache and message broker.",
			Icon:        "server",
			Image:       "redis:7-alpine",
			DefaultPort: 6379,
		},
		{
			ID:          "n8n",
			Name:        "n8n Workflow Automation",
			Category:    "Productivity",
			Description: "Fair-code workflow automation tool with 400+ native app integrations.",
			Icon:        "activity",
			Image:       "n8nio/n8n:latest",
			DefaultPort: 5678,
			DefaultEnv: map[string]string{
				"N8N_PORT": "5678",
			},
		},
		{
			ID:          "uptime-kuma",
			Name:        "Uptime Kuma",
			Category:    "Monitoring",
			Description: "Self-hosted monitoring tool with status pages and alert channels.",
			Icon:        "activity",
			Image:       "louislam/uptime-kuma:1",
			DefaultPort: 3001,
		},
		{
			ID:          "plausible",
			Name:        "Plausible Analytics CE",
			Category:    "Analytics",
			Description: "Lightweight, privacy-friendly Google Analytics alternative.",
			Icon:        "activity",
			Image:       "ghcr.io/plausible/community-edition:latest",
			DefaultPort: 8000,
		},
		{
			ID:          "whoami",
			Name:        "Traefik Whoami",
			Category:    "Tool",
			Description: "Tiny Go webserver that echoes OS and HTTP request details.",
			Icon:        "info",
			Image:       "traefik/whoami",
			DefaultPort: 80,
		},
	}
}

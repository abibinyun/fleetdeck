package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"fleetdeck/internal/domain"
	"fleetdeck/internal/keeper"
	"fleetdeck/internal/store"
	"fleetdeck/internal/ui"
)

type mockDockerCommander struct{}

func (m *mockDockerCommander) ListContainers(ctx context.Context, node *domain.Node) ([]domain.Container, error) {
	return []domain.Container{
		{ID: "c1", Name: "app-prod", Image: "nginx:alpine", State: "running", Status: "Up 2 hours"},
	}, nil
}
func (m *mockDockerCommander) RestartContainer(ctx context.Context, node *domain.Node, name string) error {
	return nil
}
func (m *mockDockerCommander) StartContainer(ctx context.Context, node *domain.Node, name string) error {
	return nil
}
func (m *mockDockerCommander) StopContainer(ctx context.Context, node *domain.Node, name string) error {
	return nil
}
func (m *mockDockerCommander) StreamLogs(ctx context.Context, node *domain.Node, name string, tail int) (<-chan string, error) {
	ch := make(chan string, 1)
	ch <- "mock log output line 1"
	close(ch)
	return ch, nil
}
func (m *mockDockerCommander) DeployApp(ctx context.Context, node *domain.Node, app *domain.Application) error {
	return nil
}
func (m *mockDockerCommander) DeleteApp(ctx context.Context, node *domain.Node, app *domain.Application) error {
	return nil
}

func setupTestServer(t *testing.T) (*Server, func()) {
	tmpDir, err := os.MkdirTemp("", "fleetdeck-srv-*")
	if err != nil {
		t.Fatalf("temp dir err: %v", err)
	}

	appStore, err := store.NewAppStore(tmpDir)
	if err != nil {
		t.Fatalf("store err: %v", err)
	}

	nodes := []domain.Node{
		{
			ID:       "node-local",
			Name:     "Test Node",
			Provider: domain.ProviderLocalDocker,
			DockerAccess: domain.DockerAccessConfig{
				Driver: "local_socket",
			},
		},
	}

	engine := keeper.NewEngine(nodes, nil, &mockDockerCommander{}, appStore, nil, time.Minute)
	srv := NewServer(engine, &mockDockerCommander{}, ui.TemplateFS, "", "")

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}
	return srv, cleanup
}

func TestHealthzEndpoint(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("ok")) {
		t.Errorf("expected ok in body, got %s", rec.Body.String())
	}
}

func TestNodesAndTemplatesAPI(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. GET /api/nodes
	reqNodes := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	recNodes := httptest.NewRecorder()
	srv.Routes().ServeHTTP(recNodes, reqNodes)

	if recNodes.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", recNodes.Code)
	}
	var nodes []domain.Node
	if err := json.Unmarshal(recNodes.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("failed to decode nodes: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != "node-local" {
		t.Errorf("unexpected nodes returned: %+v", nodes)
	}

	// 2. GET /api/templates
	reqTmpl := httptest.NewRequest(http.MethodGet, "/api/templates", nil)
	recTmpl := httptest.NewRecorder()
	srv.Routes().ServeHTTP(recTmpl, reqTmpl)

	if recTmpl.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", recTmpl.Code)
	}
	var templates []domain.Template
	if err := json.Unmarshal(recTmpl.Body.Bytes(), &templates); err != nil {
		t.Fatalf("failed to decode templates: %v", err)
	}
	if len(templates) < 3 {
		t.Errorf("expected at least 3 templates, got %d", len(templates))
	}
}

func TestApplicationDeployAndLifecycle(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. POST /api/apps (Deploy New App)
	payload := `{"name":"landing-page","node_id":"node-local","type":"template","template_id":"nginx-static"}`
	reqDeploy := httptest.NewRequest(http.MethodPost, "/api/apps", bytes.NewBufferString(payload))
	reqDeploy.Header.Set("Content-Type", "application/json")
	recDeploy := httptest.NewRecorder()
	srv.Routes().ServeHTTP(recDeploy, reqDeploy)

	if recDeploy.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recDeploy.Code, recDeploy.Body.String())
	}
	var created domain.Application
	if err := json.Unmarshal(recDeploy.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal app err: %v", err)
	}
	if created.Status != domain.AppStatusRunning {
		t.Errorf("expected status running, got %s", created.Status)
	}
	if created.Domain == "" {
		t.Errorf("expected auto-generated domain, got empty")
	}

	// 2. GET /api/apps (List apps)
	reqList := httptest.NewRequest(http.MethodGet, "/api/apps", nil)
	recList := httptest.NewRecorder()
	srv.Routes().ServeHTTP(recList, reqList)

	var appList []domain.Application
	_ = json.Unmarshal(recList.Body.Bytes(), &appList)
	if len(appList) != 1 {
		t.Errorf("expected 1 app, got %d", len(appList))
	}

	// 3. DELETE /api/apps/{id}
	reqDel := httptest.NewRequest(http.MethodDelete, "/api/apps/"+created.ID, nil)
	recDel := httptest.NewRecorder()
	srv.Routes().ServeHTTP(recDel, reqDel)

	if recDel.Code != http.StatusOK {
		t.Errorf("expected 200 on delete, got %d", recDel.Code)
	}
}

func TestGitHubWebhookTrigger(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// Deploy an app with git_repo
	app := &domain.Application{
		ID:      "app-git-1",
		Name:    "my-git-app",
		NodeID:  "node-local",
		Type:    domain.AppTypeDockerImage,
		GitRepo: "https://github.com/myorg/awesome-repo.git",
	}
	_ = srv.engine.GetAppStore().Save(app)

	// Simulate GitHub Push Webhook
	hookPayload := `{
		"ref": "refs/heads/main",
		"repository": {
			"name": "awesome-repo",
			"full_name": "myorg/awesome-repo",
			"clone_url": "https://github.com/myorg/awesome-repo.git"
		},
		"head_commit": {
			"id": "commit123",
			"message": "fix bug"
		}
	}`
	reqHook := httptest.NewRequest(http.MethodPost, "/api/webhooks/github", bytes.NewBufferString(hookPayload))
	reqHook.Header.Set("Content-Type", "application/json")
	recHook := httptest.NewRecorder()
	srv.Routes().ServeHTTP(recHook, reqHook)

	if recHook.Code != http.StatusOK {
		t.Errorf("expected 200 from webhook, got %d", recHook.Code)
	}
	if !bytes.Contains(recHook.Body.Bytes(), []byte("triggered")) {
		t.Errorf("expected triggered response, got %s", recHook.Body.String())
	}
}

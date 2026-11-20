package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"fleetdeck/internal/domain"
	"fleetdeck/internal/keeper"
)

type dummyDocker struct{}

func (d *dummyDocker) ListContainers(ctx context.Context, node *domain.Node) ([]domain.Container, error) {
	return []domain.Container{{ID: "c1", Name: "app1", State: "running"}}, nil
}
func (d *dummyDocker) RestartContainer(ctx context.Context, node *domain.Node, name string) error {
	return nil
}
func (d *dummyDocker) StartContainer(ctx context.Context, node *domain.Node, name string) error {
	return nil
}
func (d *dummyDocker) StopContainer(ctx context.Context, node *domain.Node, name string) error {
	return nil
}
func (d *dummyDocker) StreamLogs(ctx context.Context, node *domain.Node, name string, tail int) (<-chan string, error) {
	ch := make(chan string)
	close(ch)
	return ch, nil
}
func (d *dummyDocker) DeployApp(ctx context.Context, node *domain.Node, app *domain.Application) error {
	return nil
}
func (d *dummyDocker) DeleteApp(ctx context.Context, node *domain.Node, app *domain.Application) error {
	return nil
}

func TestMCPInitializeAndListTools(t *testing.T) {
	nodes := []domain.Node{{ID: "local", Name: "Local"}}
	engine := keeper.NewEngine(nodes, nil, &dummyDocker{}, nil, nil, time.Minute)
	h := NewHandler(engine)

	// 1. Initialize
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(initReq))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var initResp JSONRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &initResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if initResp.Error != nil {
		t.Fatalf("unexpected rpc error: %+v", initResp.Error)
	}

	// 2. tools/list
	toolsReq := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(toolsReq))
	h.ServeHTTP(rec2, req2)

	var toolsResp JSONRPCResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &toolsResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if toolsResp.Result == nil {
		t.Fatalf("expected result in tools/list")
	}
}

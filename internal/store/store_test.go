package store

import (
	"os"
	"testing"

	"fleetdeck/internal/domain"
)

func TestAppStoreCRUD(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fleetdeck-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	s, err := NewAppStore(tmpDir)
	if err != nil {
		t.Fatalf("NewAppStore error: %v", err)
	}

	app := &domain.Application{
		ID:     "test-app-1",
		Name:   "my-web",
		Type:   domain.AppTypeDockerImage,
		NodeID: "node-1",
		Status: domain.AppStatusRunning,
	}

	// Save
	if err := s.Save(app); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	// Get
	retrieved := s.Get("test-app-1")
	if retrieved == nil || retrieved.Name != "my-web" {
		t.Fatalf("expected my-web, got %+v", retrieved)
	}

	// List
	list := s.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 app, got %d", len(list))
	}

	// Reopen from disk to test persistence
	s2, err := NewAppStore(tmpDir)
	if err != nil {
		t.Fatalf("reopen error: %v", err)
	}
	if s2.Get("test-app-1") == nil {
		t.Fatalf("expected persisted app, got nil")
	}

	// Delete
	if err := s.Delete("test-app-1"); err != nil {
		t.Fatalf("delete error: %v", err)
	}
	if s.Get("test-app-1") != nil {
		t.Fatalf("expected nil after delete")
	}
}

func TestDefaultTemplates(t *testing.T) {
	templates := GetDefaultTemplates()
	if len(templates) == 0 {
		t.Fatalf("expected default templates, got 0")
	}
	for _, tmpl := range templates {
		if tmpl.ID == "" || tmpl.Image == "" || tmpl.DefaultPort == 0 {
			t.Errorf("invalid template %+v", tmpl)
		}
	}
}

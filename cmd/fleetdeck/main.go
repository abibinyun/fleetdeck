package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"fleetdeck/internal/builder"
	"fleetdeck/internal/config"
	"fleetdeck/internal/docker"
	"fleetdeck/internal/domain"
	"fleetdeck/internal/keeper"
	"fleetdeck/internal/provider"
	"fleetdeck/internal/server"
	"fleetdeck/internal/store"
	"fleetdeck/internal/ui"
)

func main() {
	configPath := flag.String("config", "fleet.yaml", "Path to fleet YAML configuration")
	dataDir := flag.String("data", "data", "Directory to persist application store metadata")
	flag.Parse()

	log.Println("[FleetDeck] Initializing Hybrid Fleet Control Plane & Open PaaS Engine...")

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// 1. Initialize Providers (Local Docker, InstaBox, Generic SSH VPS)
	drivers := map[domain.NodeProvider]domain.NodeDriver{
		domain.ProviderLocalDocker: provider.NewLocalDockerDriver(),
		domain.ProviderInstaBox:    provider.NewInstaBoxDriver(),
		domain.ProviderGenericSSH:  provider.NewGenericSSHDriver(),
	}

	// 2. Initialize Agentless Docker Commander
	dockerCommander := docker.NewAgentlessCommander()

	// 3. Initialize Persistent Application Store (PaaS Engine)
	absDataDir, _ := filepath.Abs(*dataDir)
	appStore, err := store.NewAppStore(absDataDir)
	if err != nil {
		log.Fatalf("Failed to initialize app store: %v", err)
	}

	// 4. Initialize Git Builder (Vercel-like Git repo build engine)
	buildWorkDir := filepath.Join(absDataDir, "builds")
	gitBuilder := builder.NewGitBuilder(buildWorkDir)

	// 5. Initialize Autonomous Keeper Engine
	pollDuration := time.Duration(cfg.Server.PollInterval) * time.Second
	engine := keeper.NewEngine(cfg.Nodes, drivers, dockerCommander, appStore, gitBuilder, pollDuration)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine.Start(ctx)

	// 6. Initialize HTTP & MCP Server
	srv := server.NewServer(
		engine,
		dockerCommander,
		ui.TemplateFS,
		cfg.Server.AuthUsername,
		cfg.Server.AuthPassword,
	)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      srv.Routes(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	go func() {
		log.Printf("[FleetDeck] Web Dashboard ready at http://%s", addr)
		log.Printf("[FleetDeck] AI Agent MCP Endpoint active at http://%s/mcp", addr)
		log.Printf("[FleetDeck] GitHub Webhook active at http://%s/api/webhooks/github", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[FleetDeck] Shutting down gracefully...")
	engine.Stop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)

	log.Println("[FleetDeck] Bye.")
}

package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"fleetdeck/internal/domain"
	"fleetdeck/internal/keeper"
	"fleetdeck/internal/mcp"
	"fleetdeck/internal/security"
	"fleetdeck/internal/store"
)

// Server handles HTTP API, MCP endpoint, GitHub Webhooks, and Web Dashboard rendering
type Server struct {
	engine       *keeper.Engine
	docker       domain.DockerCommander
	mcpHandler   *mcp.Handler
	rateLimiter  *security.RateLimiter
	templateFS   embed.FS
	authUsername string
	authPassword string
}

func NewServer(engine *keeper.Engine, docker domain.DockerCommander, tfs embed.FS, user, pass string) *Server {
	return &Server{
		engine:       engine,
		docker:       docker,
		mcpHandler:   mcp.NewHandler(engine),
		rateLimiter:  security.NewRateLimiter(15, time.Minute), // Max 15 sensitive calls per min per IP
		templateFS:   tfs,
		authUsername: user,
		authPassword: pass,
	}
}

func (s *Server) getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) basicAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings := s.engine.GetAppStore().GetSettings()
		user := s.authUsername
		passHash := s.authPassword
		if settings.AdminUsername != "" {
			user = settings.AdminUsername
			passHash = settings.AdminPassword
		}

		if user == "" || passHash == "" {
			next(w, r)
			return
		}

		u, p, ok := r.BasicAuth()
		if !ok || u != user || !security.ComparePassword(passHash, p) {
			w.Header().Set("WWW-Authenticate", `Basic realm="FleetDeck Control Plane"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Healthcheck Endpoint
	mux.HandleFunc("/healthz", s.handleHealthz)

	// Public Webhooks & Onboarding Status
	mux.HandleFunc("/api/webhooks/github", s.handleGitHubWebhook)
	mux.HandleFunc("/api/settings/status", s.handleSettingsStatus)
	mux.HandleFunc("/api/settings/init", s.handleSettingsInit)
	mux.HandleFunc("/api/settings/sshkey", s.handleSSHKey)

	// Dashboard UI
	mux.HandleFunc("/", s.basicAuth(s.handleDashboard))

	// MCP JSON-RPC Endpoint (AI Agent access)
	mux.HandleFunc("/mcp", s.basicAuth(s.mcpHandler.ServeHTTP))

	// REST APIs - Nodes & Containers
	mux.HandleFunc("/api/nodes", s.basicAuth(s.handleNodes))
	mux.HandleFunc("/api/nodes/", s.basicAuth(s.handleNodeSubroutes))
	mux.HandleFunc("/api/nodes/bootstrap", s.basicAuth(s.handleNodeBootstrap))
	mux.HandleFunc("/api/keeper/logs", s.basicAuth(s.handleKeeperLogs))

	// REST APIs - Applications (PaaS Features)
	mux.HandleFunc("/api/apps", s.basicAuth(s.handleApps))
	mux.HandleFunc("/api/apps/", s.basicAuth(s.handleAppSubroutes))
	mux.HandleFunc("/api/templates", s.basicAuth(s.handleTemplates))

	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok","uptime":"running"}`))
}

func (s *Server) handleSSHKey(w http.ResponseWriter, r *http.Request) {
	pubPaths := []string{
		"/root/.ssh/id_ed25519.pub",
		"/home/abibinyun/.ssh/id_ed25519.pub",
	}

	var pubContent string
	for _, p := range pubPaths {
		if data, err := os.ReadFile(p); err == nil {
			pubContent = strings.TrimSpace(string(data))
			break
		}
	}

	if pubContent == "" {
		pubContent = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIK+fleetdeck-default-key root@fleetdeck"
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"public_key": pubContent,
		"install_command": fmt.Sprintf("mkdir -p ~/.ssh && echo '%s' >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys", pubContent),
	})
}

type gitHubPushEvent struct {
	Ref        string `json:"ref"`
	Repository struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
	HeadCommit struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	} `json:"head_commit"`
}

func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Read error", http.StatusBadRequest)
		return
	}

	var event gitHubPushEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Match registered application
	store := s.engine.GetAppStore()
	var targetApp *domain.Application
	for _, app := range store.List() {
		if strings.Contains(app.GitRepo, event.Repository.Name) || strings.Contains(app.GitRepo, event.Repository.FullName) {
			targetApp = app
			break
		}
	}

	if targetApp == nil {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ignored","reason":"no matching app registered"}`))
		return
	}

	// HMAC Signature Verification if WebhookKey is configured
	if targetApp.WebhookKey != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		if !security.VerifyGitHubHMAC(body, targetApp.WebhookKey, sig) {
			log.Printf("[FleetDeck Security] Invalid HMAC signature for webhook app: %s", targetApp.Name)
			http.Error(w, "Invalid webhook signature", http.StatusUnauthorized)
			return
		}
	}

	go func(app domain.Application) {
		log.Printf("[FleetDeck Auto-Deploy] Syncing %s on node %s...", app.Name, app.NodeID)
		_ = s.engine.DeployApplication(context.Background(), &app)
	}(*targetApp)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf(`{"status":"triggered","app":"%s"}`, targetApp.Name)))
}

func (s *Server) handleSettingsStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	settings := s.engine.GetAppStore().GetSettings()
	// Never expose hashed password in status API
	settings.AdminPassword = ""
	_ = json.NewEncoder(w).Encode(settings)
}

func (s *Server) handleSettingsInit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Rate limit init endpoint
	clientIP := s.getClientIP(r)
	if !s.rateLimiter.Allow(clientIP) {
		http.Error(w, "Rate limit exceeded. Try again in a minute.", http.StatusTooManyRequests)
		return
	}

	var st domain.SystemSettings
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if st.AdminUsername == "" || st.AdminPassword == "" {
		http.Error(w, "Username and password required", http.StatusBadRequest)
		return
	}

	// Hash password securely before saving
	hashedPass, err := security.GenerateSaltedHash(st.AdminPassword)
	if err != nil {
		http.Error(w, "Password hashing failed", http.StatusInternalServerError)
		return
	}
	st.AdminPassword = hashedPass

	_ = s.engine.GetAppStore().SaveSettings(st)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"initialized"}`))
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	tmpl, err := template.ParseFS(s.templateFS, "templates/index.html")
	if err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, nil)
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		nodes := s.engine.GetNodes()
		_ = json.NewEncoder(w).Encode(nodes)

	case http.MethodPost:
		var node domain.Node
		if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		if err := security.ValidateHost(node.DockerAccess.Target); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if node.ID == "" {
			node.ID = fmt.Sprintf("node-%d", time.Now().Unix())
		}
		if node.Name == "" {
			http.Error(w, "Server name is required", http.StatusBadRequest)
			return
		}
		if node.Provider == "" {
			node.Provider = domain.ProviderGenericSSH
		}
		if node.DockerAccess.Driver == "" {
			node.DockerAccess.Driver = "ssh"
		}

		err := s.engine.AddNode(r.Context(), &node)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(node)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

type bootstrapPayload struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
}

func (s *Server) handleNodeBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientIP := s.getClientIP(r)
	if !s.rateLimiter.Allow(clientIP) {
		http.Error(w, "Rate limit exceeded. Please wait.", http.StatusTooManyRequests)
		return
	}

	var p bootstrapPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate target host against command injection
	if err := security.ValidateHost(p.Host); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	msg, err := s.engine.BootstrapRemoteNode(r.Context(), p.Host, p.Port, p.User)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "error", "message": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "message": msg})
}

type actionPayload struct {
	Action string `json:"action"`
	Target string `json:"target,omitempty"`
}

func (s *Server) handleNodeSubroutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if r.Method == http.MethodPost && (len(path) > 11 && path[len(path)-7:] == "/action") {
		s.handleNodeAction(w, r)
		return
	}

	if r.Method == http.MethodGet && (len(path) > 5 && path[len(path)-5:] == "/logs") {
		s.handleContainerLogs(w, r)
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleNodeAction(w http.ResponseWriter, r *http.Request) {
	var payload actionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	nodeID := trimSubpath(r.URL.Path, "/api/nodes/", "/action")
	err := s.engine.TriggerAction(r.Context(), nodeID, payload.Action, payload.Target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 6 {
		http.Error(w, "Invalid log path", http.StatusBadRequest)
		return
	}
	nodeID := parts[2]
	containerName := parts[4]

	tail := 100
	if qTail := r.URL.Query().Get("tail"); qTail != "" {
		if val, err := strconv.Atoi(qTail); err == nil {
			tail = val
		}
	}

	node := s.engine.FindNode(nodeID)
	if node == nil {
		http.Error(w, "Node not found", http.StatusNotFound)
		return
	}

	ch, err := s.docker.StreamLogs(r.Context(), node, containerName, tail)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for line := range ch {
		_, _ = fmt.Fprintf(w, "%s\n", line)
	}
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	store := s.engine.GetAppStore()

	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(store.List())

	case http.MethodPost:
		var app domain.Application
		if err := json.NewDecoder(r.Body).Decode(&app); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Security: Validate App Slug to avoid injection
		if err := security.ValidateSlug(app.Name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if app.ID == "" {
			app.ID = fmt.Sprintf("app-%d", time.Now().Unix())
		}

		// Auto-generate domain if empty
		if app.Domain == "" {
			settings := store.GetSettings()
			root := settings.RootDomain
			if root != "" {
				app.Domain = fmt.Sprintf("%s.%s", app.Name, root)
			} else {
				node := s.engine.FindNode(app.NodeID)
				hostIP := "127.0.0.1"
				if node != nil && node.DockerAccess.Target != "" && !strings.Contains(node.DockerAccess.Target, "/") {
					hostIP = node.DockerAccess.Target
				}
				app.Domain = fmt.Sprintf("%s.%s.sslip.io", app.Name, hostIP)
			}
		}

		err := s.engine.DeployApplication(r.Context(), &app)
		if err != nil {
			http.Error(w, fmt.Sprintf("Deploy failed: %v", err), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(app)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAppSubroutes(w http.ResponseWriter, r *http.Request) {
	appID := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	if appID == "" {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		err := s.engine.DeleteApplication(r.Context(), appID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"deleted"}`))

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(store.GetDefaultTemplates())
}

func (s *Server) handleKeeperLogs(w http.ResponseWriter, r *http.Request) {
	paths := []string{
		"/homelab/logs/keeper.log",
		"/home/abibinyun/data/homelab/logs/keeper.log",
	}

	var data []byte
	var err error
	for _, p := range paths {
		if content, readErr := os.ReadFile(p); readErr == nil {
			lines := strings.Split(string(content), "\n")
			start := len(lines) - 80
			if start < 0 {
				start = 0
			}
			data = []byte(strings.Join(lines[start:], "\n"))
			err = nil
			break
		} else {
			err = readErr
		}
	}

	if err != nil && len(data) == 0 {
		http.Error(w, fmt.Sprintf("Keeper log unavailable: %v", err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(data)
}

func trimSubpath(full, prefix, suffix string) string {
	res := full
	if len(prefix) > 0 && len(res) >= len(prefix) && res[:len(prefix)] == prefix {
		res = res[len(prefix):]
	}
	if len(suffix) > 0 && len(res) >= len(suffix) && res[len(res)-len(suffix):] == suffix {
		res = res[:len(res)-len(suffix)]
	}
	return res
}

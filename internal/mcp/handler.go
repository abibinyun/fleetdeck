package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"fleetdeck/internal/domain"
	"fleetdeck/internal/healing"
	"fleetdeck/internal/keeper"
)

// MCP JSON-RPC 2.0 structures
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

type Handler struct {
	engine *keeper.Engine
}

func NewHandler(engine *keeper.Engine) *Handler {
	return &Handler{engine: engine}
}

// ServeHTTP handles MCP protocol over HTTP POST (standard for web-accessible MCP)
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed. MCP requires POST.", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Read error", http.StatusBadRequest)
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		resp := JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error:   &RPCError{Code: -32700, Message: "Parse error"},
		}
		writeJSON(w, resp)
		return
	}

	ctx := r.Context()
	result, rpcErr := h.handleMethod(ctx, req.Method, req.Params)

	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
		Error:   rpcErr,
	}
	writeJSON(w, resp)
}

func (h *Handler) handleMethod(ctx context.Context, method string, rawParams json.RawMessage) (interface{}, *RPCError) {
	switch method {
	case "initialize":
		return map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]string{
				"name":    "fleetdeck-mcp",
				"version": "1.0.0",
			},
			"capabilities": map[string]interface{}{
				"tools": map[string]bool{"listChanged": true},
			},
		}, nil

	case "tools/list":
		return map[string]interface{}{
			"tools": h.listTools(),
		}, nil

	case "fleet_diagnose_crash":
		// Diagnose container crash logs and suggest remedy
		var args struct {
			ContainerName string `json:"container_name"`
			Logs          string `json:"logs"`
		}
		if err := json.Unmarshal(rawParams, &args); err == nil {
			cause, fix := healing.Diagnose(args.ContainerName, args.Logs)
			return map[string]interface{}{
				"root_cause":    cause,
				"suggested_fix": fix,
			}, nil
		}
		return nil, &RPCError{Code: -32602, Message: "Invalid diagnose params"}

	case "tools/call":
		var callParams struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(rawParams, &callParams); err != nil {
			return nil, &RPCError{Code: -32602, Message: "Invalid call params"}
		}
		return h.executeTool(ctx, callParams.Name, callParams.Arguments)

	default:
		return nil, &RPCError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", method)}
	}
}

func (h *Handler) listTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "fleet_list_nodes",
			Description: "List all compute nodes (Homeserver, VPS NAT, Instabox) and their running containers and resource metrics.",
			InputSchema: map[string]interface{}{
				"type": "object",
			},
		},
		{
			Name:        "fleet_deploy_app",
			Description: "Deploy a new application container (Nginx, Docker image, or 1-Click stack) to any node with auto-routing.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":        map[string]interface{}{"type": "string", "description": "Application name (slug, e.g. my-app)"},
					"node_id":     map[string]interface{}{"type": "string", "description": "Target node id (e.g. homeserver-local, vps-nat-prod, instabox-1)"},
					"app_type":    map[string]interface{}{"type": "string", "description": "static_nginx, docker_image, or template"},
					"image":       map[string]interface{}{"type": "string", "description": "Docker image tag (e.g. redis:7-alpine, nginx:alpine)"},
					"domain":      map[string]interface{}{"type": "string", "description": "Custom ingress domain (e.g. test.example.com)"},
					"port":        map[string]interface{}{"type": "integer", "description": "Internal container target port (e.g. 80, 3000, 8080)"},
					"static_html": map[string]interface{}{"type": "string", "description": "Optional HTML snippet for static_nginx type"},
				},
				"required": []string{"name", "node_id", "app_type"},
			},
		},
		{
			Name:        "fleet_container_action",
			Description: "Perform lifecycle action on a container (restart, start, stop) on a specific node.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"node_id":        map[string]interface{}{"type": "string", "description": "Node ID hosting the container"},
					"container_name": map[string]interface{}{"type": "string", "description": "Exact container name"},
					"action":         map[string]interface{}{"type": "string", "enum": []string{"restart", "start", "stop"}},
				},
				"required": []string{"node_id", "container_name", "action"},
			},
		},
		{
			Name:        "fleet_diagnose_crash",
			Description: "Analyze container crash logs, identify root cause (OOM, port conflict, missing env, panic), and return suggested recovery action.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"container_name": map[string]interface{}{"type": "string", "description": "Target container name"},
					"logs":           map[string]interface{}{"type": "string", "description": "Raw container tail logs"},
				},
				"required": []string{"container_name", "logs"},
			},
		},
	}
}

func (h *Handler) executeTool(ctx context.Context, name string, args map[string]interface{}) (interface{}, *RPCError) {
	switch name {
	case "fleet_list_nodes":
		nodes := h.engine.GetNodes()
		return map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": toJSONString(nodes)},
			},
		}, nil

	case "fleet_deploy_app":
		appName, _ := args["name"].(string)
		nodeID, _ := args["node_id"].(string)
		appType, _ := args["app_type"].(string)
		image, _ := args["image"].(string)
		domainStr, _ := args["domain"].(string)
		staticHTML, _ := args["static_html"].(string)
		var port int
		if p, ok := args["port"].(float64); ok {
			port = int(p)
		}

		if appName == "" || nodeID == "" {
			return nil, &RPCError{Code: -32602, Message: "name and node_id are required"}
		}

		app := &domain.Application{
			ID:         fmt.Sprintf("app-%d", time.Now().Unix()),
			Name:       appName,
			NodeID:     nodeID,
			Type:       domain.AppType(appType),
			Image:      image,
			Domain:     domainStr,
			Port:       port,
			StaticHTML: staticHTML,
		}

		err := h.engine.DeployApplication(ctx, app)
		if err != nil {
			return nil, &RPCError{Code: -32000, Message: fmt.Sprintf("deploy failed: %v", err)}
		}

		return map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": fmt.Sprintf("Application '%s' successfully deployed on node '%s'. Domain: %s", app.Name, app.NodeID, app.Domain)},
			},
		}, nil

	case "fleet_container_action":
		nodeID, _ := args["node_id"].(string)
		cName, _ := args["container_name"].(string)
		action, _ := args["action"].(string)

		err := h.engine.TriggerAction(ctx, nodeID, "docker_"+action, cName)
		if err != nil {
			return nil, &RPCError{Code: -32000, Message: fmt.Sprintf("action failed: %v", err)}
		}

		return map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": fmt.Sprintf("Successfully executed '%s' on '%s' (%s)", action, cName, nodeID)},
			},
		}, nil

	case "fleet_diagnose_crash":
		cName, _ := args["container_name"].(string)
		rawLogs, _ := args["logs"].(string)
		cause, fix := healing.Diagnose(cName, rawLogs)
		return map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": fmt.Sprintf("Root Cause: %s\nSuggested Fix: %s", cause, fix)},
			},
		}, nil

	default:
		return nil, &RPCError{Code: -32601, Message: fmt.Sprintf("Unknown tool: %s", name)}
	}
}

func toJSONString(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

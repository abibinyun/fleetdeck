package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"fleetdeck/internal/domain"
)

type InstaBoxDriver struct {
	baseURL    string
	httpClient *http.Client
}

func NewInstaBoxDriver() *InstaBoxDriver {
	return &InstaBoxDriver{
		baseURL: "https://api.instabox.sh/v1",
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

type machineResponse struct {
	ID            string  `json:"id"`
	State         string  `json:"state"`
	RunStartedAt  *string `json:"runStartedAt"`
	MaxRunMinutes *int    `json:"maxRunMinutes"`
	CooldownUntil *string `json:"cooldownUntil"`
}

func (d *InstaBoxDriver) getHeaders(apiKey string) http.Header {
	h := make(http.Header)
	h.Set("X-API-Key", apiKey)
	h.Set("Content-Type", "application/json")
	h.Set("Idempotency-Key", fmt.Sprintf("fleetdeck-%d", time.Now().UnixNano()))
	return h
}

func (d *InstaBoxDriver) GetState(ctx context.Context, node *domain.Node) (domain.NodeState, error) {
	telemetry, err := d.GetTelemetry(ctx, node)
	if err != nil {
		return domain.StateUnknown, err
	}
	return telemetry.State, nil
}

func (d *InstaBoxDriver) GetTelemetry(ctx context.Context, node *domain.Node) (*domain.NodeTelemetry, error) {
	mid := node.ProviderConfig["machine_id"]
	key := node.ProviderConfig["api_key"]
	if mid == "" || key == "" {
		return nil, fmt.Errorf("missing machine_id or api_key in provider_config")
	}

	url := fmt.Sprintf("%s/machines/%s", d.baseURL, mid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header = d.getHeaders(key)

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch machine info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("instabox API error, status: %d", resp.StatusCode)
	}

	var m machineResponse
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}

	state := domain.NodeState(m.State)

	// Check Cooldown
	var cdTime *time.Time
	if m.CooldownUntil != nil && *m.CooldownUntil != "" && *m.CooldownUntil != "null" {
		t, err := time.Parse(time.RFC3339Nano, *m.CooldownUntil)
		if err == nil && t.After(time.Now()) {
			state = domain.StateCooldown
			cdTime = &t
		}
	}

	// Calculate uptime
	uptimeMinutes := 0
	if m.RunStartedAt != nil && *m.RunStartedAt != "" && *m.RunStartedAt != "null" {
		t, err := time.Parse(time.RFC3339Nano, *m.RunStartedAt)
		if err == nil {
			uptimeMinutes = int(time.Since(t).Minutes())
		}
	}

	maxRun := 120
	if m.MaxRunMinutes != nil {
		maxRun = *m.MaxRunMinutes
	} else if confMax, ok := node.ProviderConfig["max_run_minutes"]; ok {
		if val, err := strconv.Atoi(confMax); err == nil {
			maxRun = val
		}
	}

	return &domain.NodeTelemetry{
		State:         state,
		UptimeMinutes: uptimeMinutes,
		MaxRunMinutes: maxRun,
		CooldownUntil: cdTime,
	}, nil
}

func (d *InstaBoxDriver) Keepalive(ctx context.Context, node *domain.Node) error {
	mid := node.ProviderConfig["machine_id"]
	key := node.ProviderConfig["api_key"]

	url := fmt.Sprintf("%s/machines/%s/activity-leases/keepalive", d.baseURL, mid)
	payload := []byte(`{"ttlSeconds":900}`)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}
	req.Header = d.getHeaders(key)

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send keepalive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("keepalive failed with HTTP %d", resp.StatusCode)
	}
	return nil
}

func (d *InstaBoxDriver) Wake(ctx context.Context, node *domain.Node) error {
	mid := node.ProviderConfig["machine_id"]
	key := node.ProviderConfig["api_key"]

	url := fmt.Sprintf("%s/machines/%s/resume", d.baseURL, mid)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header = d.getHeaders(key)

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send resume: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wake failed with HTTP %d", resp.StatusCode)
	}
	return nil
}

func (d *InstaBoxDriver) Suspend(ctx context.Context, node *domain.Node) error {
	mid := node.ProviderConfig["machine_id"]
	key := node.ProviderConfig["api_key"]

	url := fmt.Sprintf("%s/machines/%s/suspend", d.baseURL, mid)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header = d.getHeaders(key)

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send suspend: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("suspend failed with HTTP %d", resp.StatusCode)
	}
	return nil
}

func (d *InstaBoxDriver) Stop(ctx context.Context, node *domain.Node) error {
	mid := node.ProviderConfig["machine_id"]
	key := node.ProviderConfig["api_key"]

	url := fmt.Sprintf("%s/machines/%s/stop", d.baseURL, mid)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header = d.getHeaders(key)

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send stop: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("stop failed with HTTP %d", resp.StatusCode)
	}
	return nil
}

func (d *InstaBoxDriver) Start(ctx context.Context, node *domain.Node) error {
	mid := node.ProviderConfig["machine_id"]
	key := node.ProviderConfig["api_key"]

	url := fmt.Sprintf("%s/machines/%s/start", d.baseURL, mid)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header = d.getHeaders(key)

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send start: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("start failed with HTTP %d", resp.StatusCode)
	}
	return nil
}

func (d *InstaBoxDriver) RestartCycle(ctx context.Context, node *domain.Node) error {
	// Idempotent & robust cycle rotation: Stop -> poll stopped -> Start with retry
	_ = d.Stop(ctx, node)

	// Wait up to 15s for machine to transition to stopped/suspended
	for i := 0; i < 5; i++ {
		time.Sleep(2 * time.Second)
		st, err := d.GetState(ctx, node)
		if err == nil && (st == domain.StateStopped || st == domain.StateSuspended) {
			break
		}
	}

	// Start with retry
	var startErr error
	for attempt := 1; attempt <= 3; attempt++ {
		startErr = d.Start(ctx, node)
		if startErr == nil {
			return nil
		}
		// Also try wake/resume if start returned error
		if wakeErr := d.Wake(ctx, node); wakeErr == nil {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return startErr
}

// Package sensors polls external door/window sensors and publishes their
// state to the daemon's state store under sensor.<name>.
package sensors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/mcd/lastwatt/internal/actions"
	"github.com/mcd/lastwatt/internal/config"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

// WindowPoller polls a single Shelly Gen2 input and writes sensor.<name>
// to the state store as "open" or "closed" on each successful read.
type WindowPoller struct {
	cfg   config.WindowSensor
	store actions.StateStore
	log   *slog.Logger
}

// NewWindowPoller builds a poller for the given sensor config.
func NewWindowPoller(cfg config.WindowSensor, store actions.StateStore, log *slog.Logger) *WindowPoller {
	return &WindowPoller{cfg: cfg, store: store, log: log}
}

// Run polls on the configured interval. Blocks until ctx is cancelled.
func (p *WindowPoller) Run(ctx context.Context) error {
	p.log.Info("window sensor poller starting",
		"name", p.cfg.Name, "host", p.cfg.Host, "interval", p.cfg.Interval)

	p.pollOnce(ctx)

	ticker := time.NewTicker(p.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			p.pollOnce(ctx)
		}
	}
}

func (p *WindowPoller) pollOnce(ctx context.Context) {
	state, err := p.read(ctx)
	if err != nil {
		p.log.Warn("window sensor read failed", "name", p.cfg.Name, "error", err)
		return
	}
	p.store.Set("sensor."+p.cfg.Name, state)
	p.log.Debug("window sensor", "name", p.cfg.Name, "state", state)
}

// read queries the Shelly Gen2 RPC endpoint and returns "open" or "closed".
// Shelly's Input.GetStatus reports state=true when the circuit is closed.
// With a normally-closed reed switch (magnet present => circuit closed),
// state=true => window closed. The invert flag flips this convention for
// normally-open reed switches.
func (p *WindowPoller) read(ctx context.Context) (string, error) {
	url := fmt.Sprintf("http://%s/rpc/Input.GetStatus?id=%d", p.cfg.Host, p.cfg.Input)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var r struct {
		State *bool `json:"state"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	if r.State == nil {
		return "", fmt.Errorf("no state field in response: %s", string(body))
	}

	closed := *r.State
	if p.cfg.Invert {
		closed = !closed
	}
	if closed {
		return "closed", nil
	}
	return "open", nil
}

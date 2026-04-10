package shelly

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mcd/lastwatt/internal/actions"
)

func init() {
	actions.Register(&setAction{})
}

var client = &http.Client{Timeout: 10 * time.Second}

type setAction struct{}

func (a *setAction) Name() string { return "shelly.set" }

func (a *setAction) Validate(params map[string]any) error {
	if _, ok := params["host"]; !ok {
		return fmt.Errorf("missing required param: host")
	}
	st, ok := params["state"]
	if !ok {
		return fmt.Errorf("missing required param: state")
	}
	s := fmt.Sprintf("%v", st)
	if s != "on" && s != "off" {
		return fmt.Errorf("state must be 'on' or 'off', got: %s", s)
	}
	return nil
}

func (a *setAction) Execute(ctx context.Context, params map[string]any, store actions.StateStore) error {
	host := fmt.Sprintf("%v", params["host"])
	st := fmt.Sprintf("%v", params["state"])

	url := fmt.Sprintf("http://%s/relay/0?turn=%s", host, st)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("shelly.set: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("shelly.set %s: %w", host, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("shelly.set %s: HTTP %d", host, resp.StatusCode)
	}

	return nil
}

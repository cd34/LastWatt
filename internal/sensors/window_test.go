package sensors

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcd/lastwatt/internal/config"
	"github.com/mcd/lastwatt/internal/state"
)

var testLog = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))

func newTestStore(t *testing.T) *state.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := state.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestWindowPoller_ClosedAndOpen(t *testing.T) {
	cases := []struct {
		name       string
		shellyResp string
		invert     bool
		want       string
	}{
		{"state true => closed", `{"id":0,"state":true}`, false, "closed"},
		{"state false => open", `{"id":0,"state":false}`, false, "open"},
		{"inverted state true => open", `{"id":0,"state":true}`, true, "open"},
		{"inverted state false => closed", `{"id":0,"state":false}`, true, "closed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.URL.Path, "/rpc/Input.GetStatus") {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				w.Write([]byte(c.shellyResp))
			}))
			defer srv.Close()

			u, _ := url.Parse(srv.URL)
			store := newTestStore(t)
			p := NewWindowPoller(config.WindowSensor{
				Name:     "test",
				Host:     u.Host,
				Input:    0,
				Interval: time.Second,
				Invert:   c.invert,
			}, store, testLog)

			got, err := p.read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestWindowPoller_PollOnceWritesStore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":0,"state":false}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	store := newTestStore(t)
	p := NewWindowPoller(config.WindowSensor{
		Name:     "front",
		Host:     u.Host,
		Interval: time.Second,
	}, store, testLog)

	p.pollOnce(context.Background())

	v, ok := store.Get("sensor.front")
	if !ok || v != "open" {
		t.Fatalf("expected sensor.front=open, got %q (ok=%v)", v, ok)
	}
}

func TestWindowPoller_ReadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	store := newTestStore(t)
	p := NewWindowPoller(config.WindowSensor{
		Name: "bad", Host: u.Host, Interval: time.Second,
	}, store, testLog)

	if _, err := p.read(context.Background()); err == nil {
		t.Fatal("expected error on HTTP 500")
	}
	// store should not have a value
	if _, ok := store.Get("sensor.bad"); ok {
		t.Fatal("store should not be written on error")
	}
}

// avoid unused-import error if state init changes
var _ = os.Stderr

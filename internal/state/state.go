package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Status represents the current curtailment state.
type Status string

const (
	StatusNormal    Status = "normal"
	StatusCurtailed Status = "curtailed"
	StatusUnknown   Status = "unknown"
)

// Store persists curtailment state and action key-value data to a JSON file.
// Writes are debounced — dirty data is flushed at most once per second.
type Store struct {
	mu    sync.RWMutex
	path  string
	data  storeData
	dirty bool
	done  chan struct{}
}

type storeData struct {
	Status   Status            `json:"status"`
	Since    time.Time         `json:"since"`
	LastPing time.Time         `json:"last_ping"`
	Values   map[string]string `json:"values"`
}

func New(path string) (*Store, error) {
	s := &Store{
		path: path,
		data: storeData{
			Status: StatusUnknown,
			Values: make(map[string]string),
		},
		done: make(chan struct{}),
	}

	// Try to load existing state. A missing or empty file (e.g. truncated by a
	// crash mid-write) starts fresh rather than failing — losing state just means
	// re-deriving it on the next ping, but refusing to start defeats the daemon.
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &s.data); err != nil {
			return nil, fmt.Errorf("parsing state file: %w", err)
		}
		if s.data.Values == nil {
			s.data.Values = make(map[string]string)
		}
	}

	// Start background flush loop
	go s.flushLoop()

	return s, nil
}

// Close flushes any pending writes and stops the background loop.
func (s *Store) Close() error {
	close(s.done)
	return s.Flush()
}

// Flush forces an immediate write if dirty.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirty {
		s.dirty = false
		return s.writeToDisk()
	}
	return nil
}

// Reload re-reads state from disk, picking up changes made by other processes.
func (s *Store) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("reloading state: %w", err)
	}
	var loaded storeData
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("parsing state on reload: %w", err)
	}
	if loaded.Values == nil {
		loaded.Values = make(map[string]string)
	}
	s.data = loaded
	s.dirty = false
	return nil
}

func (s *Store) flushLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.mu.Lock()
			if s.dirty {
				s.dirty = false
				s.writeToDisk() // best-effort, errors logged by caller
			}
			s.mu.Unlock()
		}
	}
}

func (s *Store) markDirty() {
	s.dirty = true
}

func (s *Store) GetStatus() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Status
}

// SetStatus updates the curtailment status and flushes immediately
// since this is a critical state change.
func (s *Store) SetStatus(status Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Status = status
	s.data.Since = time.Now()
	return s.writeToDisk()
}

func (s *Store) SetLastPing(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.LastPing = t
	s.markDirty()
}

func (s *Store) Since() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Since
}

func (s *Store) LastPing() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.LastPing
}

// Get implements actions.StateStore.
func (s *Store) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data.Values[key]
	return v, ok
}

// Set implements actions.StateStore.
func (s *Store) Set(key string, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Values[key] = value
	s.markDirty()
	return nil
}

func (s *Store) writeToDisk() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0600)
}

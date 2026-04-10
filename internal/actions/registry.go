package actions

import (
	"context"
	"fmt"
	"sync"
)

// Action is the interface that all curtailment actions must implement.
type Action interface {
	Name() string
	Execute(ctx context.Context, params map[string]any, store StateStore) error
	Validate(params map[string]any) error
}

// StateStore provides key-value persistence for actions that need to save
// and restore state (e.g., saving thermostat mode before curtailment).
type StateStore interface {
	Get(key string) (string, bool)
	Set(key string, value string) error
}

var (
	mu       sync.RWMutex
	registry = make(map[string]Action)
)

// Register adds an action to the global registry.
func Register(a Action) {
	mu.Lock()
	defer mu.Unlock()
	registry[a.Name()] = a
}

// Get returns a registered action by name.
func Get(name string) (Action, error) {
	mu.RLock()
	defer mu.RUnlock()
	a, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown action: %s", name)
	}
	return a, nil
}

// List returns the names of all registered actions.
func List() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

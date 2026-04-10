package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Monitor   MonitorConfig  `yaml:"monitor"`
	Location  LocationConfig `yaml:"location"`
	StateFile string         `yaml:"state_file"`
	Curtail   []ActionStep   `yaml:"curtail"`
	Restore   []ActionStep   `yaml:"restore"`
	Schedules []Schedule     `yaml:"schedules,omitempty"`
	Overrides []OverrideRule `yaml:"overrides,omitempty"`
}

type Schedule struct {
	Name    string       `yaml:"name"`
	Days    []string     `yaml:"days"`
	Start   string       `yaml:"start"`
	Stop    string       `yaml:"stop"`
	Actions []ActionStep `yaml:"actions"`
	Restore []ActionStep `yaml:"restore"`
}

type LocationConfig struct {
	Lat              float64       `yaml:"lat"`
	Lon              float64       `yaml:"lon"`
	ForecastInterval time.Duration `yaml:"forecast_interval"`
}

type MonitorConfig struct {
	Host             string        `yaml:"host"`
	Interval         time.Duration `yaml:"interval"`
	FailThreshold    int           `yaml:"fail_threshold"`
	RecoverThreshold int           `yaml:"recover_threshold"`
}

type ActionStep struct {
	Action string         `yaml:"action"`
	Params map[string]any `yaml:"params,omitempty"`
}

type OverrideRule struct {
	Trigger    string        `yaml:"trigger"`
	Actions    []ActionStep  `yaml:"actions"`
	RevertAfter time.Duration `yaml:"revert_after,omitempty"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	cfg := &Config{
		// Defaults
		Monitor: MonitorConfig{
			Interval:         5 * time.Second,
			FailThreshold:    3,
			RecoverThreshold: 2,
		},
		StateFile: "/var/lib/lastwatt/state.json",
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if cfg.Monitor.Host == "" {
		return nil, fmt.Errorf("monitor.host is required")
	}

	return cfg, nil
}

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
	Rates     RatesConfig    `yaml:"rates,omitempty"`
	Schedules []Schedule     `yaml:"schedules,omitempty"`
	Vacation  VacationConfig `yaml:"vacation,omitempty"`
	Overrides []OverrideRule `yaml:"overrides,omitempty"`

	ratesLocation *time.Location // parsed from Rates.Timezone
}

// RatesLocation returns the parsed timezone for rate schedules, or local time.
func (c *Config) RatesLocation() *time.Location {
	if c.ratesLocation != nil {
		return c.ratesLocation
	}
	return time.Local
}

// RatesConfig defines time-of-use electricity rate windows.
type RatesConfig struct {
	Timezone       string       `yaml:"timezone"`
	WeekendsOffpeak bool        `yaml:"weekends_offpeak"`
	Peak           *RateWindow  `yaml:"peak,omitempty"`
	MidPeak        *RateWindow  `yaml:"mid_peak,omitempty"`
	FlowOverride   bool         `yaml:"flow_override"`
	Curtail        []ActionStep `yaml:"curtail,omitempty"`
	Restore        []ActionStep `yaml:"restore,omitempty"`
}

// RateWindow defines a daily time window for a rate tier.
type RateWindow struct {
	Start string `yaml:"start"` // "HH:MM" in 24h format
	End   string `yaml:"end"`   // "HH:MM" in 24h format
}

type VacationConfig struct {
	PollInterval time.Duration `yaml:"poll_interval,omitempty"`
	Curtail      []ActionStep  `yaml:"curtail,omitempty"`
	Restore      []ActionStep  `yaml:"restore,omitempty"`
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

// RateSchedules converts the rates config into scheduler-compatible schedules.
func (r RatesConfig) RateSchedules() []Schedule {
	if r.Peak == nil && r.MidPeak == nil {
		return nil
	}

	weekdays := []string{"Mon", "Tue", "Wed", "Thu", "Fri"}
	allDays := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

	days := allDays
	if r.WeekendsOffpeak {
		days = weekdays
	}

	var schedules []Schedule

	if r.MidPeak != nil && r.MidPeak.Start != "" && r.MidPeak.End != "" {
		schedules = append(schedules, Schedule{
			Name:    "mid-peak",
			Days:    days,
			Start:   r.MidPeak.Start,
			Stop:    r.MidPeak.End,
			Actions: r.Curtail,
			Restore: r.Restore,
		})
	}

	if r.Peak != nil && r.Peak.Start != "" && r.Peak.End != "" {
		schedules = append(schedules, Schedule{
			Name:    "peak",
			Days:    days,
			Start:   r.Peak.Start,
			Stop:    r.Peak.End,
			Actions: r.Curtail,
			Restore: r.Restore,
		})
	}

	return schedules
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

	// Merge rate-based schedules into the schedules list
	if rateScheds := cfg.Rates.RateSchedules(); len(rateScheds) > 0 {
		cfg.Schedules = append(cfg.Schedules, rateScheds...)
	}

	// Load timezone for rate schedules
	if cfg.Rates.Timezone != "" {
		loc, err := time.LoadLocation(cfg.Rates.Timezone)
		if err != nil {
			return nil, fmt.Errorf("invalid rates timezone %q: %w", cfg.Rates.Timezone, err)
		}
		cfg.ratesLocation = loc
	}

	return cfg, nil
}

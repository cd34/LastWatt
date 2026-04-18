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
	Grid      GridConfig       `yaml:"grid"`
	Rates     RatesConfig      `yaml:"rates,omitempty"`
	Vacation  VacationConfig   `yaml:"vacation,omitempty"`
	FlowMeter *FlowMeterConfig `yaml:"flow_meter,omitempty"`
	Schedules []Schedule     `yaml:"schedules,omitempty"`
	Overrides []OverrideRule `yaml:"overrides,omitempty"`

	ratesLocation *time.Location // parsed from Rates.Timezone
}

// GridConfig defines actions to run on grid power loss and recovery.
type GridConfig struct {
	Start []ActionStep `yaml:"start"` // actions to run when grid goes down
	Stop  []ActionStep `yaml:"stop"`  // actions to run when grid comes back
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
	Timezone        string       `yaml:"timezone"`
	WeekendsOffpeak bool         `yaml:"weekends_offpeak"`
	Peak            *RateWindow  `yaml:"peak,omitempty"`
	MidPeak         *RateWindow  `yaml:"mid_peak,omitempty"`
	Start           []ActionStep `yaml:"start,omitempty"` // actions when entering a rate window
	Stop            []ActionStep `yaml:"stop,omitempty"`  // actions when leaving a rate window
}

// RateWindow defines a daily time window for a rate tier.
type RateWindow struct {
	Start string `yaml:"start"` // "HH:MM" in 24h format
	End   string `yaml:"end"`   // "HH:MM" in 24h format
}

type VacationConfig struct {
	PollInterval time.Duration `yaml:"poll_interval,omitempty"`
	Start        []ActionStep  `yaml:"start,omitempty"` // actions when vacation detected
	Stop         []ActionStep  `yaml:"stop,omitempty"`  // actions when vacation ends
}

type Schedule struct {
	Name   string        `yaml:"name"`
	Days   []string      `yaml:"days"`
	Begin  string        `yaml:"begin"` // "HH:MM" window start
	End    string        `yaml:"end"`   // "HH:MM" window end
	Jitter time.Duration `yaml:"jitter,omitempty"`
	Start  []ActionStep  `yaml:"start"` // actions when entering window
	Stop   []ActionStep  `yaml:"stop"`  // actions when leaving window
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

// FlowMeterConfig defines the connection to a TUF-2000M flow meter.
type FlowMeterConfig struct {
	Port     string        `yaml:"port"`      // serial port (default /dev/ttyUSB0)
	Baud     int           `yaml:"baud"`      // baud rate (default 9600)
	SlaveID  int           `yaml:"slave_id"`  // Modbus slave address (default 1)
	Interval time.Duration `yaml:"interval"`  // poll interval (default 5s)
}

type ActionStep struct {
	Action       string         `yaml:"action"`
	Params       map[string]any `yaml:"params,omitempty"`
	FlowOverride bool           `yaml:"flow_override,omitempty"`
}

// FlowOverrideSteps returns only the steps that have flow_override set.
func FlowOverrideSteps(steps []ActionStep) []ActionStep {
	var out []ActionStep
	for _, s := range steps {
		if s.FlowOverride {
			out = append(out, s)
		}
	}
	return out
}

// HasFlowOverride returns true if any step has flow_override set.
func HasFlowOverride(steps []ActionStep) bool {
	for _, s := range steps {
		if s.FlowOverride {
			return true
		}
	}
	return false
}

// FlowOverridePair returns the start and stop steps that participate in
// flow override.
func FlowOverridePair(start, stop []ActionStep) (startSteps, stopSteps []ActionStep) {
	return FlowOverrideSteps(start), FlowOverrideSteps(stop)
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
			Name:  "mid-peak",
			Days:  days,
			Begin: r.MidPeak.Start,
			End:   r.MidPeak.End,
			Start: r.Start,
			Stop:  r.Stop,
		})
	}

	if r.Peak != nil && r.Peak.Start != "" && r.Peak.End != "" {
		schedules = append(schedules, Schedule{
			Name:  "peak",
			Days:  days,
			Begin: r.Peak.Start,
			End:   r.Peak.End,
			Start: r.Start,
			Stop:  r.Stop,
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

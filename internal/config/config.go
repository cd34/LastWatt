package config

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Location      LocationConfig   `yaml:"location"`
	StateFile     string           `yaml:"state_file"`
	Grid          GridConfig       `yaml:"grid"`
	Rates         RatesConfig      `yaml:"rates,omitempty"`
	Vacation      VacationConfig   `yaml:"vacation,omitempty"`
	FlowMeter     *FlowMeterConfig `yaml:"flow_meter,omitempty"`
	WindowSensors []WindowSensor   `yaml:"window_sensors,omitempty"`
	Schedules     []Schedule       `yaml:"schedules,omitempty"`
	Triggers      []TriggerConfig  `yaml:"triggers,omitempty"`

	ratesLocation *time.Location // parsed from Rates.Timezone
}

// WindowSensor configures polling for a single Shelly-based door/window sensor.
// "gen2" is the Shelly Plus RPC API (e.g. Shelly Plus i4 with a reed switch on
// an input). The sensor reports state via sensor.<name> as "open" or "closed".
type WindowSensor struct {
	Name     string        `yaml:"name"`
	Host     string        `yaml:"host"`            // ip or hostname (no scheme)
	API      string        `yaml:"api,omitempty"`   // "gen2" (default); future: "gen1", "blu"
	Input    int           `yaml:"input,omitempty"` // input index (Gen2 default 0)
	Interval time.Duration `yaml:"interval,omitempty"`
	Invert   bool          `yaml:"invert,omitempty"` // flip open/closed (e.g. NO reed switch)
}

// GridConfig defines the grid monitor and actions to run on power loss/recovery.
type GridConfig struct {
	Monitor MonitorConfig `yaml:"monitor"`
	Start   []ActionStep  `yaml:"start"` // actions to run when grid goes down
	Stop    []ActionStep  `yaml:"stop"`  // actions to run when grid comes back
}

// TriggerConfig defines a condition-based trigger that watches store values.
type TriggerConfig struct {
	Name         string       `yaml:"name"`
	When         []string     `yaml:"when"`                    // conditions: "key op value"
	Unless       []string     `yaml:"unless,omitempty"`        // suppress trigger when all of these are also true
	Start        []ActionStep `yaml:"start"`                   // actions when conditions met
	Stop         []ActionStep `yaml:"stop"`                    // actions when conditions clear
	RespectHolds *bool        `yaml:"respect_holds,omitempty"` // default true; skip stop if hold active
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
	Port     string        `yaml:"port"`     // serial port (default /dev/ttyUSB0)
	Baud     int           `yaml:"baud"`     // baud rate (default 9600)
	SlaveID  int           `yaml:"slave_id"` // Modbus slave address (default 1)
	Interval time.Duration `yaml:"interval"` // poll interval (default 5s)
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
		StateFile: "/var/lib/lastwatt/state.json",
		Grid: GridConfig{
			Monitor: MonitorConfig{
				Interval:         5 * time.Second,
				FailThreshold:    3,
				RecoverThreshold: 2,
			},
		},
	}

	// Strict decoding: unknown/renamed keys (e.g. a stale top-level "monitor:"
	// or "curtail:" from an older schema) become explicit errors instead of
	// being silently ignored.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if cfg.Grid.Monitor.Host == "" {
		return nil, fmt.Errorf("grid.monitor.host is required")
	}

	for i, ws := range cfg.WindowSensors {
		if ws.Name == "" {
			return nil, fmt.Errorf("window_sensors[%d]: name is required", i)
		}
		if ws.Host == "" {
			return nil, fmt.Errorf("window_sensors[%q]: host is required", ws.Name)
		}
		if ws.API == "" {
			cfg.WindowSensors[i].API = "gen2"
		} else if ws.API != "gen2" {
			return nil, fmt.Errorf("window_sensors[%q]: api %q not supported (use \"gen2\")", ws.Name, ws.API)
		}
		if ws.Interval == 0 {
			cfg.WindowSensors[i].Interval = 30 * time.Second
		}
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

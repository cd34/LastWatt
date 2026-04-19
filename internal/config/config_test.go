package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRateSchedules_PeakAndMidPeak(t *testing.T) {
	r := RatesConfig{
		Timezone:        "America/Denver",
		WeekendsOffpeak: true,
		Peak:            &RateWindow{Start: "17:00", End: "21:00"},
		MidPeak:         &RateWindow{Start: "13:00", End: "17:00"},
		Start:           []ActionStep{{Action: "gpio.set"}},
		Stop:            []ActionStep{{Action: "gpio.set"}},
	}

	scheds := r.RateSchedules()
	if len(scheds) != 2 {
		t.Fatalf("expected 2 schedules, got %d", len(scheds))
	}

	// Mid-peak first
	if scheds[0].Name != "mid-peak" {
		t.Errorf("expected first schedule 'mid-peak', got %q", scheds[0].Name)
	}
	if scheds[0].Begin != "13:00" || scheds[0].End != "17:00" {
		t.Errorf("mid-peak times wrong: %s-%s", scheds[0].Begin, scheds[0].End)
	}

	// Peak second
	if scheds[1].Name != "peak" {
		t.Errorf("expected second schedule 'peak', got %q", scheds[1].Name)
	}
	if scheds[1].Begin != "17:00" || scheds[1].End != "21:00" {
		t.Errorf("peak times wrong: %s-%s", scheds[1].Begin, scheds[1].End)
	}
}

func TestRateSchedules_WeekendsOffpeak(t *testing.T) {
	r := RatesConfig{
		WeekendsOffpeak: true,
		Peak:            &RateWindow{Start: "17:00", End: "21:00"},
	}

	scheds := r.RateSchedules()
	if len(scheds) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(scheds))
	}

	days := scheds[0].Days
	if len(days) != 5 {
		t.Fatalf("expected 5 weekdays, got %d: %v", len(days), days)
	}
	for _, d := range days {
		if d == "Sat" || d == "Sun" {
			t.Errorf("weekend day %q should not be included", d)
		}
	}
}

func TestRateSchedules_WeekendsIncluded(t *testing.T) {
	r := RatesConfig{
		WeekendsOffpeak: false,
		Peak:            &RateWindow{Start: "17:00", End: "21:00"},
	}

	scheds := r.RateSchedules()
	days := scheds[0].Days
	if len(days) != 7 {
		t.Fatalf("expected 7 days, got %d: %v", len(days), days)
	}
}

func TestRateSchedules_Empty(t *testing.T) {
	r := RatesConfig{}
	scheds := r.RateSchedules()
	if len(scheds) != 0 {
		t.Fatalf("expected 0 schedules, got %d", len(scheds))
	}
}

func TestRateSchedules_PeakOnly(t *testing.T) {
	r := RatesConfig{
		Peak: &RateWindow{Start: "17:00", End: "21:00"},
	}

	scheds := r.RateSchedules()
	if len(scheds) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(scheds))
	}
	if scheds[0].Name != "peak" {
		t.Errorf("expected 'peak', got %q", scheds[0].Name)
	}
}

func TestRateSchedules_MidPeakOnly(t *testing.T) {
	r := RatesConfig{
		MidPeak: &RateWindow{Start: "13:00", End: "17:00"},
	}

	scheds := r.RateSchedules()
	if len(scheds) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(scheds))
	}
	if scheds[0].Name != "mid-peak" {
		t.Errorf("expected 'mid-peak', got %q", scheds[0].Name)
	}
}

func TestLoad_RatesTimezone(t *testing.T) {
	yaml := `
grid:
  monitor:
    host: 192.168.1.1
rates:
  timezone: America/Denver
  weekends_offpeak: true
  peak:
    start: "17:00"
    end: "21:00"
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte(yaml), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	loc := cfg.RatesLocation()
	if loc.String() != "America/Denver" {
		t.Fatalf("expected America/Denver, got %q", loc.String())
	}

	// Rate schedules should have been merged into Schedules
	if len(cfg.Schedules) != 1 {
		t.Fatalf("expected 1 schedule (peak), got %d", len(cfg.Schedules))
	}
	if cfg.Schedules[0].Name != "peak" {
		t.Errorf("expected 'peak', got %q", cfg.Schedules[0].Name)
	}
}

func TestLoad_RatesInvalidTimezone(t *testing.T) {
	yaml := `
grid:
  monitor:
    host: 192.168.1.1
rates:
  timezone: Not/ATimezone
  peak:
    start: "17:00"
    end: "21:00"
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte(yaml), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid timezone")
	}
}

package forecast

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	nwsBase   = "https://api.weather.gov"
	userAgent = "(lastwatt, github.com/mcd/lastwatt)"
)

// HourlyPeriod is one hour of forecast data.
type HourlyPeriod struct {
	StartTime    time.Time
	TempF        int
	Humidity     int     // percent
	WindSpeedMPH int     // parsed from string like "10 mph"
	WindDir      string  // "S", "NNW", etc.
	PrecipPct    int     // probability of precipitation
	DewpointC    float64
	Short        string  // "Sunny", "Chance Rain Showers", etc.
	IsDaytime    bool
}

// Forecast holds the current hourly forecast.
type Forecast struct {
	Updated time.Time
	Periods []HourlyPeriod
}

// TodayHigh returns the highest forecasted temp for the rest of today.
func (f *Forecast) TodayHigh() int {
	if f == nil || len(f.Periods) == 0 {
		return 0
	}
	now := time.Now()
	today := now.Format("2006-01-02")
	high := -999
	for _, p := range f.Periods {
		if p.StartTime.Format("2006-01-02") != today {
			continue
		}
		if p.StartTime.Before(now) {
			continue
		}
		if p.TempF > high {
			high = p.TempF
		}
	}
	if high == -999 {
		return 0
	}
	return high
}

// TempAtHour returns the forecasted temp at a specific hour today (0-23).
func (f *Forecast) TempAtHour(hour int) (int, bool) {
	if f == nil {
		return 0, false
	}
	today := time.Now().Format("2006-01-02")
	for _, p := range f.Periods {
		if p.StartTime.Format("2006-01-02") == today && p.StartTime.Hour() == hour {
			return p.TempF, true
		}
	}
	return 0, false
}

// Provider fetches and caches NWS forecasts.
type Provider struct {
	lat, lon    float64
	forecastURL string // cached from points lookup
	log         *slog.Logger
	client      *http.Client

	mu      sync.RWMutex
	current *Forecast
}

func NewProvider(lat, lon float64, log *slog.Logger) *Provider {
	return &Provider{
		lat: lat,
		lon: lon,
		log: log,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Latest returns the most recent forecast, or nil.
func (p *Provider) Latest() *Forecast {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.current
}

// Run polls the NWS API on an interval. Blocks until ctx is cancelled.
func (p *Provider) Run(ctx context.Context, interval time.Duration) error {
	// Fetch immediately on start
	if err := p.fetch(ctx); err != nil {
		p.log.Warn("initial forecast fetch failed", "error", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := p.fetch(ctx); err != nil {
				p.log.Warn("forecast fetch failed", "error", err)
			}
		}
	}
}

func (p *Provider) fetch(ctx context.Context) error {
	// Step 1: resolve grid (cached after first call)
	if p.forecastURL == "" {
		url := fmt.Sprintf("%s/points/%.4f,%.4f", nwsBase, p.lat, p.lon)
		body, err := p.nwsGet(ctx, url)
		if err != nil {
			return fmt.Errorf("points lookup: %w", err)
		}

		var points struct {
			Properties struct {
				ForecastHourly string `json:"forecastHourly"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(body, &points); err != nil {
			return fmt.Errorf("points parse: %w", err)
		}
		if points.Properties.ForecastHourly == "" {
			return fmt.Errorf("no forecastHourly URL in points response")
		}
		p.forecastURL = points.Properties.ForecastHourly
		p.log.Info("NWS grid resolved", "url", p.forecastURL)
	}

	// Step 2: fetch hourly forecast
	body, err := p.nwsGet(ctx, p.forecastURL)
	if err != nil {
		return fmt.Errorf("hourly forecast: %w", err)
	}

	var resp struct {
		Properties struct {
			Updated string `json:"updated"`
			Periods []struct {
				StartTime                string `json:"startTime"`
				Temperature              int    `json:"temperature"`
				WindSpeed                string `json:"windSpeed"`
				WindDirection             string `json:"windDirection"`
				ShortForecast            string `json:"shortForecast"`
				IsDaytime                bool   `json:"isDaytime"`
				ProbabilityOfPrecipitation struct {
					Value *int `json:"value"`
				} `json:"probabilityOfPrecipitation"`
				RelativeHumidity struct {
					Value *int `json:"value"`
				} `json:"relativeHumidity"`
				Dewpoint struct {
					Value *float64 `json:"value"`
				} `json:"dewpoint"`
			} `json:"periods"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("forecast parse: %w", err)
	}

	updated, _ := time.Parse(time.RFC3339, resp.Properties.Updated)

	periods := make([]HourlyPeriod, 0, len(resp.Properties.Periods))
	for _, rp := range resp.Properties.Periods {
		start, _ := time.Parse(time.RFC3339, rp.StartTime)
		hp := HourlyPeriod{
			StartTime:    start,
			TempF:        rp.Temperature,
			WindSpeedMPH: parseWindSpeed(rp.WindSpeed),
			WindDir:       rp.WindDirection,
			Short:        rp.ShortForecast,
			IsDaytime:    rp.IsDaytime,
		}
		if rp.ProbabilityOfPrecipitation.Value != nil {
			hp.PrecipPct = *rp.ProbabilityOfPrecipitation.Value
		}
		if rp.RelativeHumidity.Value != nil {
			hp.Humidity = *rp.RelativeHumidity.Value
		}
		if rp.Dewpoint.Value != nil {
			hp.DewpointC = *rp.Dewpoint.Value
		}
		periods = append(periods, hp)
	}

	f := &Forecast{
		Updated: updated,
		Periods: periods,
	}

	p.mu.Lock()
	p.current = f
	p.mu.Unlock()

	if len(periods) > 0 {
		high := f.TodayHigh()
		p.log.Info("forecast updated",
			"periods", len(periods),
			"next_hour_temp", fmt.Sprintf("%d°F", periods[0].TempF),
			"today_high", fmt.Sprintf("%d°F", high),
			"next_hour", periods[0].Short,
		)
	}

	return nil
}

func (p *Provider) nwsGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/geo+json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("NWS API %d: %s", resp.StatusCode, string(body[:min(200, len(body))]))
	}

	return body, nil
}

// parseWindSpeed extracts the first number from strings like "10 mph" or "5 to 10 mph".
func parseWindSpeed(s string) int {
	parts := strings.Fields(s)
	for _, p := range parts {
		if n, err := strconv.Atoi(p); err == nil {
			return n
		}
	}
	return 0
}

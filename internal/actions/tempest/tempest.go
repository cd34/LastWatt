package tempest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/mcd/lastwatt/internal/actions"
)

const udpPort = 50222

// obs_st field indices
const (
	idxTimestamp   = 0
	idxWindLull    = 1
	idxWindAvg     = 2
	idxWindGust    = 3
	idxWindDir     = 4
	idxPressure    = 6
	idxTempC       = 7
	idxHumidity    = 8
	idxIlluminance = 9
	idxUV          = 10
	idxSolarRad    = 11
	idxRainMin     = 12
	idxBattery     = 16
	idxDailyRain   = 18
)

func init() {
	actions.Register(&readAction{})
}

// Observation holds the latest parsed Tempest data.
type Observation struct {
	Timestamp   time.Time
	TempC       float64
	TempF       float64
	Humidity    float64
	PressureMB  float64
	WindAvgMPS  float64
	WindGustMPS float64
	WindDir     float64
	SolarRad    float64
	UV          float64
	RainMinMM   float64
	DailyRainMM float64
	Battery     float64
}

// Listener runs a background UDP listener that keeps the latest observation.
type Listener struct {
	mu     sync.RWMutex
	latest *Observation
	log    *slog.Logger
}

// global singleton — started once, shared by actions and the daemon
var (
	listener     *Listener
	listenerOnce sync.Once
)

// GetListener returns the global Tempest listener, starting it if needed.
func GetListener(log *slog.Logger) *Listener {
	listenerOnce.Do(func() {
		listener = &Listener{log: log}
	})
	return listener
}

// Latest returns the most recent observation, or nil if none received yet.
func (l *Listener) Latest() *Observation {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.latest
}

// Run listens for Tempest UDP broadcasts. Blocks until ctx is cancelled.
func (l *Listener) Run(ctx context.Context) error {
	conn, err := net.ListenPacket("udp4", fmt.Sprintf("0.0.0.0:%d", udpPort))
	if err != nil {
		return fmt.Errorf("tempest: listen UDP %d: %w", udpPort, err)
	}
	defer conn.Close()

	l.log.Info("tempest listener started", "port", udpPort)

	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			l.log.Error("tempest: UDP read error", "error", err)
			continue
		}

		l.handleMessage(buf[:n])
	}
}

type udpMessage struct {
	Type string          `json:"type"`
	Obs  [][]json.Number `json:"obs,omitempty"`
}

func (l *Listener) handleMessage(data []byte) {
	var msg udpMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		l.log.Warn("tempest: failed to parse UDP message", "error", err)
		return
	}

	l.log.Debug("tempest: received message", "type", msg.Type)

	if msg.Type != "obs_st" || len(msg.Obs) == 0 {
		return
	}

	obs := msg.Obs[0]
	if len(obs) < 17 { // need at least through battery (index 16)
		return
	}

	tempC := jsonFloat(obs[idxTempC])
	o := &Observation{
		Timestamp:   time.Unix(int64(jsonFloat(obs[idxTimestamp])), 0),
		TempC:       tempC,
		TempF:       tempC*9.0/5.0 + 32.0,
		Humidity:    jsonFloat(obs[idxHumidity]),
		PressureMB:  jsonFloat(obs[idxPressure]),
		WindAvgMPS:  jsonFloat(obs[idxWindAvg]),
		WindGustMPS: jsonFloat(obs[idxWindGust]),
		WindDir:     jsonFloat(obs[idxWindDir]),
		SolarRad:    jsonFloat(obs[idxSolarRad]),
		UV:          jsonFloat(obs[idxUV]),
		RainMinMM:   jsonFloat(obs[idxRainMin]),
		Battery:     jsonFloat(obs[idxBattery]),
	}
	if len(obs) > int(idxDailyRain) {
		o.DailyRainMM = jsonFloat(obs[idxDailyRain])
	}

	l.mu.Lock()
	l.latest = o
	l.mu.Unlock()

	l.log.Debug("tempest observation",
		"temp_f", fmt.Sprintf("%.1f", o.TempF),
		"humidity", fmt.Sprintf("%.0f%%", o.Humidity),
		"wind_mph", fmt.Sprintf("%.1f", o.WindAvgMPS*2.237),
	)
}

func jsonFloat(n json.Number) float64 {
	f, _ := n.Float64()
	return f
}

// readAction reads the latest Tempest observation and stores values for use by other actions.
type readAction struct{}

func (a *readAction) Name() string                  { return "tempest.read" }
func (a *readAction) Validate(map[string]any) error { return nil }

func (a *readAction) Execute(ctx context.Context, params map[string]any, store actions.StateStore) error {
	l := GetListener(slog.Default())

	// If listener isn't running yet, do inline listen for one obs_st
	obs := l.Latest()
	if obs == nil {
		conn, err := net.ListenPacket("udp4", fmt.Sprintf("0.0.0.0:%d", udpPort))
		if err != nil {
			return fmt.Errorf("tempest: listen UDP %d: %w", udpPort, err)
		}
		defer conn.Close()

		deadline := time.Now().Add(90 * time.Second)
		conn.SetReadDeadline(deadline)
		buf := make([]byte, 4096)

		for time.Now().Before(deadline) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					return fmt.Errorf("tempest: no observation received within timeout — is the Tempest hub on this network?")
				}
				continue
			}
			l.handleMessage(buf[:n])
			if obs = l.Latest(); obs != nil {
				break
			}
		}
		if obs == nil {
			return fmt.Errorf("tempest: received packets but no obs_st observation — Tempest may still be initializing")
		}
	}

	// Store values for decision-making by other actions
	store.Set("tempest.temp_f", fmt.Sprintf("%.1f", obs.TempF))
	store.Set("tempest.temp_c", fmt.Sprintf("%.1f", obs.TempC))
	store.Set("tempest.humidity", fmt.Sprintf("%.0f", obs.Humidity))
	store.Set("tempest.wind_mph", fmt.Sprintf("%.1f", obs.WindAvgMPS*2.237))
	store.Set("tempest.solar_rad", fmt.Sprintf("%.0f", obs.SolarRad))
	store.Set("tempest.battery", fmt.Sprintf("%.2f", obs.Battery))

	fmt.Printf("Tempest: %.1f°F, %.0f%% humidity, wind %.1f mph, solar %.0f W/m²\n",
		obs.TempF, obs.Humidity, obs.WindAvgMPS*2.237, obs.SolarRad)

	return nil
}

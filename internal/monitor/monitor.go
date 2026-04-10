package monitor

import (
	"context"
	"log/slog"
	"time"

	probing "github.com/prometheus-community/pro-bing"
)

// State represents the monitor's view of the host.
type State int

const (
	StateUnknown State = iota
	StateUp
	StateDown
)

func (s State) String() string {
	switch s {
	case StateUp:
		return "up"
	case StateDown:
		return "down"
	default:
		return "unknown"
	}
}

// TransitionFunc is called when the monitor detects a state change.
type TransitionFunc func(from, to State)

// PingFunc is called after each ping attempt with the result.
type PingFunc func(ok bool)

// Config holds monitor parameters.
type Config struct {
	Host             string
	Interval         time.Duration
	FailThreshold    int
	RecoverThreshold int
	OnTransition     TransitionFunc
	OnPing           PingFunc
	Log              *slog.Logger
}

// Monitor pings a host and tracks up/down state transitions.
type Monitor struct {
	cfg             Config
	state           State
	consecutiveFail int
	consecutiveOK   int
	armed           bool // false until host is seen up at least once
}

func New(cfg Config) *Monitor {
	return &Monitor{
		cfg:   cfg,
		state: StateUnknown,
	}
}

func (m *Monitor) State() State {
	return m.state
}

// Run starts the ping loop. It blocks until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) error {
	m.cfg.Log.Info("monitor starting", "host", m.cfg.Host, "interval", m.cfg.Interval)
	m.cfg.Log.Info("waiting for host to come up before arming curtailment")

	ticker := time.NewTicker(m.cfg.Interval)
	defer ticker.Stop()

	// Do an initial ping immediately
	m.ping(ctx)

	for {
		select {
		case <-ctx.Done():
			m.cfg.Log.Info("monitor stopped")
			return ctx.Err()
		case <-ticker.C:
			m.ping(ctx)
		}
	}
}

func (m *Monitor) ping(ctx context.Context) {
	pinger, err := probing.NewPinger(m.cfg.Host)
	if err != nil {
		m.cfg.Log.Error("failed to create pinger", "error", err)
		m.recordFailure()
		return
	}

	pinger.Count = 1
	pinger.Timeout = 3 * time.Second
	pinger.SetPrivileged(false) // use unprivileged UDP ping

	err = pinger.RunWithContext(ctx)
	pinger.Stop() // clean up socket resources

	if err != nil {
		m.cfg.Log.Debug("ping failed", "host", m.cfg.Host, "error", err)
		m.recordFailure()
		return
	}

	stats := pinger.Statistics()
	if stats.PacketsRecv > 0 {
		m.cfg.Log.Debug("ping ok", "host", m.cfg.Host, "rtt", stats.AvgRtt)
		m.recordSuccess()
	} else {
		m.cfg.Log.Debug("ping no reply", "host", m.cfg.Host)
		m.recordFailure()
	}
}

func (m *Monitor) recordSuccess() {
	m.consecutiveOK++
	m.consecutiveFail = 0
	if m.cfg.OnPing != nil {
		m.cfg.OnPing(true)
	}

	if !m.armed {
		m.armed = true
		m.state = StateUp
		m.cfg.Log.Info("host is reachable — curtailment armed", "host", m.cfg.Host)
		return
	}

	if m.state != StateUp && m.consecutiveOK >= m.cfg.RecoverThreshold {
		prev := m.state
		m.state = StateUp
		m.cfg.Log.Info("host is UP", "host", m.cfg.Host, "after_pings", m.consecutiveOK)
		if m.cfg.OnTransition != nil {
			m.cfg.OnTransition(prev, StateUp)
		}
	}
}

func (m *Monitor) recordFailure() {
	m.consecutiveFail++
	m.consecutiveOK = 0

	if !m.armed {
		m.cfg.Log.Debug("host not yet reachable, not armed", "host", m.cfg.Host, "failures", m.consecutiveFail)
		return
	}

	if m.state != StateDown && m.consecutiveFail >= m.cfg.FailThreshold {
		prev := m.state
		m.state = StateDown
		m.cfg.Log.Warn("host is DOWN", "host", m.cfg.Host, "after_failures", m.consecutiveFail)
		if m.cfg.OnTransition != nil {
			m.cfg.OnTransition(prev, StateDown)
		}
	}
}

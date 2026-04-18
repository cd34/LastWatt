package flow

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/goburrow/modbus"

	"github.com/mcd/lastwatt/internal/actions"
)

// TUF-2000M Modbus registers (RTU mode)
const (
	// Flow rate in m³/h: registers 1-2 (float32, big-endian)
	regFlowRate = 1
	// Default Modbus slave address
	defaultSlaveAddr = 1
	// Default serial port
	defaultPort = "/dev/ttyUSB0"
	// Default baud rate
	defaultBaud = 9600
)

func init() {
	actions.Register(&readFlowAction{})
}

// Listener monitors the flow meter in a background loop and provides
// current flow state to the override engine.
type Listener struct {
	mu       sync.RWMutex
	flowing  bool
	flowRate float64
	lastRead time.Time
	log      *slog.Logger
	port     string
	baud     int
	slaveID  byte
	store    actions.StateStore
}

func NewListener(log *slog.Logger, port string, baud int, slaveID byte) *Listener {
	if port == "" {
		port = defaultPort
	}
	if baud == 0 {
		baud = defaultBaud
	}
	if slaveID == 0 {
		slaveID = defaultSlaveAddr
	}
	return &Listener{
		log:     log,
		port:    port,
		baud:    baud,
		slaveID: slaveID,
	}
}

// SetStore configures a state store for the listener to update on each poll.
func (l *Listener) SetStore(store actions.StateStore) {
	l.store = store
}

// Flowing returns true if water flow was detected on the last poll.
func (l *Listener) Flowing() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.flowing
}

// FlowRate returns the last measured flow rate in m³/h.
func (l *Listener) FlowRate() float64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.flowRate
}

// Run polls the flow meter at the given interval. Blocks until ctx is cancelled.
func (l *Listener) Run(ctx context.Context, interval time.Duration) error {
	l.log.Info("flow listener starting", "port", l.port, "baud", l.baud, "interval", interval)

	handler := modbus.NewRTUClientHandler(l.port)
	handler.BaudRate = l.baud
	handler.DataBits = 8
	handler.StopBits = 1
	handler.Parity = "N"
	handler.SlaveId = l.slaveID
	handler.Timeout = 5 * time.Second

	if err := handler.Connect(); err != nil {
		return fmt.Errorf("flow: connecting to %s: %w", l.port, err)
	}
	defer handler.Close()

	client := modbus.NewClient(handler)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial read
	l.poll(client)

	for {
		select {
		case <-ctx.Done():
			l.log.Info("flow listener stopped")
			return ctx.Err()
		case <-ticker.C:
			l.poll(client)
		}
	}
}

func (l *Listener) poll(client modbus.Client) {
	// TUF-2000M stores flow rate as float32 in registers 1-2
	results, err := client.ReadHoldingRegisters(regFlowRate-1, 2) // 0-indexed
	if err != nil {
		l.log.Warn("flow: modbus read failed", "error", err)
		return
	}

	if len(results) < 4 {
		l.log.Warn("flow: unexpected response length", "len", len(results))
		return
	}

	// Convert two 16-bit registers to float32 (big-endian)
	bits := binary.BigEndian.Uint32(results)
	rate := math.Float32frombits(bits)

	l.mu.Lock()
	l.flowRate = float64(rate)
	l.flowing = rate > 0.01 // threshold: >0.01 m³/h ≈ some flow
	l.lastRead = time.Now()
	l.mu.Unlock()

	if l.store != nil {
		l.store.Set("flow.rate", fmt.Sprintf("%.4f", rate))
		l.store.Set("flow.flowing", fmt.Sprintf("%t", l.flowing))
	}

	l.log.Debug("flow poll", "rate_m3h", rate, "flowing", l.flowing)
}

// readFlowAction is a one-shot action to read and display current flow state.
type readFlowAction struct{}

func (a *readFlowAction) Name() string { return "flow.read" }

func (a *readFlowAction) Validate(params map[string]any) error { return nil }

func (a *readFlowAction) Execute(ctx context.Context, params map[string]any, store actions.StateStore) error {
	port := defaultPort
	if p, ok := params["port"]; ok {
		port = fmt.Sprintf("%v", p)
	}
	baud := defaultBaud
	slaveID := byte(defaultSlaveAddr)

	handler := modbus.NewRTUClientHandler(port)
	handler.BaudRate = baud
	handler.DataBits = 8
	handler.StopBits = 1
	handler.Parity = "N"
	handler.SlaveId = slaveID
	handler.Timeout = 5 * time.Second

	if err := handler.Connect(); err != nil {
		return fmt.Errorf("flow.read: connecting to %s: %w", port, err)
	}
	defer handler.Close()

	client := modbus.NewClient(handler)

	results, err := client.ReadHoldingRegisters(regFlowRate-1, 2)
	if err != nil {
		return fmt.Errorf("flow.read: %w", err)
	}

	if len(results) < 4 {
		return fmt.Errorf("flow.read: unexpected response length: %d", len(results))
	}

	bits := binary.BigEndian.Uint32(results)
	rate := math.Float32frombits(bits)

	flowing := rate > 0.01
	store.Set("flow.rate", fmt.Sprintf("%.4f", rate))
	store.Set("flow.flowing", fmt.Sprintf("%t", flowing))

	fmt.Printf("Flow rate: %.4f m³/h (flowing: %t)\n", rate, flowing)
	return nil
}

package gpio

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mcd/lastwatt/internal/actions"

	"periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/gpio/gpioreg"
	"periph.io/x/host/v3"
)

func init() {
	actions.Register(&setAction{})
	actions.Register(&blinkAction{})
}

var (
	hostOnce    sync.Once
	hostInitErr error
)

func ensureHost() error {
	hostOnce.Do(func() {
		_, hostInitErr = host.Init()
	})
	return hostInitErr
}

func getPin(params map[string]any) (gpio.PinIO, error) {
	pinVal, ok := params["pin"]
	if !ok {
		return nil, fmt.Errorf("missing required param: pin")
	}
	pinName := fmt.Sprintf("%v", pinVal)
	pin := gpioreg.ByName(pinName)
	if pin == nil {
		return nil, fmt.Errorf("unknown GPIO pin: %s", pinName)
	}
	return pin, nil
}

// setAction sets a GPIO pin high or low.
type setAction struct{}

func (a *setAction) Name() string { return "gpio.set" }

func (a *setAction) Validate(params map[string]any) error {
	if _, ok := params["pin"]; !ok {
		return fmt.Errorf("missing required param: pin")
	}
	st, ok := params["state"]
	if !ok {
		return fmt.Errorf("missing required param: state")
	}
	s := fmt.Sprintf("%v", st)
	if s != "on" && s != "off" {
		return fmt.Errorf("state must be 'on' or 'off', got: %s", s)
	}
	return nil
}

func (a *setAction) Execute(ctx context.Context, params map[string]any, store actions.StateStore) error {
	if err := ensureHost(); err != nil {
		return err
	}
	pin, err := getPin(params)
	if err != nil {
		return err
	}

	level := gpio.High
	if fmt.Sprintf("%v", params["state"]) == "off" {
		level = gpio.Low
	}

	if err := pin.Out(level); err != nil {
		return fmt.Errorf("gpio.set pin %s: %w", pin.Name(), err)
	}
	return nil
}

// blinkAction blinks a GPIO pin.
type blinkAction struct{}

func (a *blinkAction) Name() string { return "gpio.blink" }

func (a *blinkAction) Validate(params map[string]any) error {
	if _, ok := params["pin"]; !ok {
		return fmt.Errorf("missing required param: pin")
	}
	return nil
}

func (a *blinkAction) Execute(ctx context.Context, params map[string]any, store actions.StateStore) error {
	if err := ensureHost(); err != nil {
		return err
	}
	pin, err := getPin(params)
	if err != nil {
		return err
	}

	// Blink 5 times as a visual indicator, then leave on
	for i := 0; i < 5; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := pin.Out(gpio.High); err != nil {
			return fmt.Errorf("gpio.blink pin %s: %w", pin.Name(), err)
		}
		time.Sleep(200 * time.Millisecond)
		if err := pin.Out(gpio.Low); err != nil {
			return fmt.Errorf("gpio.blink pin %s: %w", pin.Name(), err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return pin.Out(gpio.High)
}

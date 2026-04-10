# LastWatt

A grid curtailment daemon for Raspberry Pi. Monitors grid power by pinging a device on the grid side (e.g., a Shelly plug). When the device stops responding, grid power is assumed lost and curtailment actions execute. When it returns, those actions are reversed.

## Features

- **Ping-based grid monitoring** with configurable fail/recover thresholds
- **Modular action system** -- add new device types without changing the core
- **YAML recipes** for curtail and restore sequences
- **Ecobee thermostat** control via web auth (no developer API key needed)
- **GPIO** relay and LED control via periph.io
- **Shelly** relay control via local HTTP API
- **Tempest weather station** integration (local UDP broadcast)
- **NWS hourly forecast** for weather-aware decision making
- **Startup grace period** -- won't false-curtail if Pi boots before the monitored device
- **Debounced state writes** to reduce SD card wear

## Quick Start

```bash
# Build
make build

# Copy and edit config
cp config.yaml.sample config.yaml
# Edit config.yaml with your host IP, lat/lon, and action params

# Authenticate with Ecobee (one-time)
./lastwatt ecobee-auth

# Test individual actions
./lastwatt action ecobee.read_mode
./lastwatt action tempest.read
./lastwatt forecast

# Run the daemon
./lastwatt daemon

# Or install as a systemd service
sudo make install
sudo systemctl enable --now lastwatt
```

## CLI

```
lastwatt daemon              # run the monitor loop
lastwatt status              # show current state (normal/curtailed)
lastwatt run curtail         # manually trigger curtail recipe
lastwatt run restore         # manually trigger restore recipe
lastwatt action <name> [-p key=value ...]  # run a single action
lastwatt ecobee-auth         # authenticate with Ecobee
lastwatt forecast            # show NWS hourly forecast
```

## Config

See [config.yaml.sample](config.yaml.sample) for a full example. Recipes are lists of actions executed top to bottom:

```yaml
monitor:
  host: 192.168.1.X        # device on grid power
  interval: 5s
  fail_threshold: 3
  recover_threshold: 2

curtail:
  - action: ecobee.read_mode
  - action: ecobee.set_hold
    params:
      heat_temp: 55
      cool_temp: 85
  - action: gpio.set
    params:
      pin: "17"
      state: off
      label: water_heater

restore:
  - action: ecobee.resume
  - action: gpio.set
    params:
      pin: "17"
      state: on
      label: water_heater
```

## Available Actions

| Action | Description |
|---|---|
| `ecobee.read_mode` | Read and save current thermostat state |
| `ecobee.set_hold` | Set temperature hold (params: `heat_temp`, `cool_temp`) |
| `ecobee.resume` | Resume normal thermostat schedule |
| `gpio.set` | Set GPIO pin high/low (params: `pin`, `state`) |
| `gpio.blink` | Blink a GPIO pin (params: `pin`) |
| `shelly.set` | Control Shelly relay (params: `host`, `state`) |
| `tempest.read` | Read latest Tempest weather observation |

## Architecture

```
cmd/lastwatt/          CLI entry point (cobra)
internal/
  monitor/             Ping loop + state machine
  config/              YAML config loader
  engine/              Recipe executor
  state/               JSON state persistence (debounced writes)
  actions/             Action interface + registry
    ecobee/            Ecobee thermostat (Auth0 web flow)
    gpio/              GPIO relay/LED (periph.io)
    shelly/            Shelly HTTP relay
    tempest/           Tempest weather station (UDP)
  forecast/            NWS hourly forecast provider
```

## Data Sources

The daemon runs three background data feeds:

- **Ping monitor** -- grid power status via ICMP to a device on grid power
- **Tempest UDP listener** -- real-time outdoor temperature, humidity, wind, solar radiation
- **NWS forecast** -- hourly forecast for the next 7 days

## License

MIT

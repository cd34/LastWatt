# LastWatt

A grid curtailment daemon for Raspberry Pi. Monitors grid power by pinging a device on the grid side (e.g., a Shelly plug). When the device stops responding, grid power is assumed lost and curtailment actions execute. When it returns, those actions are reversed.

## Features

- **Ping-based grid monitoring** with configurable fail/recover thresholds
- **Three curtailment modes** -- off-grid, time-of-use rate schedules, and vacation
- **Time-of-use rates** -- mid-peak and peak windows with timezone/DST support, weekends off-peak option
- **Flow override** -- temporarily restores water heater when flow is detected during rate or off-grid curtailment (disabled during vacation)
- **Vacation mode** -- polls Ecobee for vacation events and curtails the water heater while away
- **Coordinated holds** -- water heater only restores when all holds (grid, schedule, vacation) are cleared
- **Modular action system** -- add new device types without changing the core
- **YAML recipes** for curtail and restore sequences
- **Ecobee thermostat** control via web auth (no developer API key needed)
- **GPIO** relay and LED control via periph.io
- **Shelly** relay control via local HTTP API
- **Tempest weather station** integration (local UDP broadcast)
- **Flow meter** -- TUF-2000M ultrasonic flow meter via Modbus RTU
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
./lastwatt action flow.read
./lastwatt forecast

# Run the daemon
./lastwatt daemon

# Or install as a systemd service
sudo make install
sudo systemctl enable --now lastwatt
```

## Curtailment Modes

LastWatt manages three independent reasons to curtail the water heater. The water heater only turns back on when **all** holds are cleared.

### Off-Grid

When the monitored device stops responding (configurable fail threshold), the `grid.curtail` recipe runs -- sets the thermostat to emergency temps, turns off the water heater, lights the curtail LED. When grid power returns, the `grid.restore` recipe reverses everything. If a rate schedule or vacation hold is still active, the water heater stays off after restore.

When `flow_override` is enabled, flow detection temporarily restores the water heater during an outage (someone is showering). It re-curtails when flow stops. Flow override is disabled during vacation.

### Time-of-Use Rates

The `rates` section defines mid-peak and peak windows:

```yaml
rates:
  timezone: America/Denver
  weekends_offpeak: true
  peak:
    start: "17:00"
    end: "21:00"
  mid_peak:
    start: "13:00"
    end: "17:00"
  flow_override: true
  curtail:
    - action: gpio.set
      params: { pin: "17", state: off, label: water_heater }
  restore:
    - action: gpio.set
      params: { pin: "17", state: on, label: water_heater }
```

When `flow_override` is enabled and water flow is detected during a rate window, the water heater is temporarily restored (someone is showering). It re-curtails when flow stops. Flow override is **disabled during vacation** -- nobody is home.

### Vacation

Polls the Ecobee API for active vacation events. When vacation mode is detected, the water heater turns off. Flow override is disabled -- no reason to heat water in an empty house. When vacation ends, the water heater only restores if the grid is up and no rate schedule is active.

```yaml
vacation:
  poll_interval: 10m
  curtail:
    - action: gpio.set
      params: { pin: "17", state: off, label: water_heater }
  restore:
    - action: gpio.set
      params: { pin: "17", state: on, label: water_heater }
```

### Interaction Matrix

| Event | Grid | Vacation | Rate Schedule | Water Heater | Flow Override |
|---|---|---|---|---|---|
| Normal operation | up | off | none | ON | n/a |
| Grid goes down | **down** | off | none | OFF | allowed |
| Flow detected, grid down | down | off | none | **ON** (temp) | active |
| Flow stops, grid down | down | off | none | OFF | deactivated |
| Rate window starts | up | off | **active** | OFF | allowed |
| Flow detected, rate active | up | off | active | **ON** (temp) | active |
| Vacation starts | up | **on** | none | OFF | **blocked** |
| Flow detected, vacation | up/down | on | any | OFF | blocked |
| Grid restores, vacation active | up | on | none | OFF | blocked |
| Grid restores, rate active | up | off | active | OFF | allowed |
| Vacation ends, rate active | up | off | active | OFF | allowed |
| Rate ends, vacation active | up | on | none | OFF | blocked |
| Mid-peak ends, peak active | up | off | active | OFF | allowed |
| All holds clear | up | off | none | ON | n/a |

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

See [config.yaml.sample](config.yaml.sample) for a full example with comments. The three curtailment modes each have their own section with `curtail` and `restore` action lists:

```yaml
grid:
  curtail: [...]     # runs on grid power loss
  restore: [...]     # runs when grid returns

rates:
  timezone: America/Denver
  weekends_offpeak: true
  peak: { start: "17:00", end: "21:00" }
  mid_peak: { start: "13:00", end: "17:00" }
  flow_override: true
  curtail: [...]     # runs when entering a rate window
  restore: [...]     # runs when leaving a rate window

vacation:
  poll_interval: 10m
  curtail: [...]     # runs when Ecobee vacation detected
  restore: [...]     # runs when vacation ends
```

## Available Actions

| Action | Description |
|---|---|
| `ecobee.read_mode` | Read and save current thermostat state (detects vacation mode) |
| `ecobee.set_hold` | Set temperature hold (params: `heat_temp`, `cool_temp`) |
| `ecobee.resume` | Resume normal thermostat schedule |
| `gpio.set` | Set GPIO pin high/low (params: `pin`, `state`, `label`) |
| `gpio.blink` | Blink a GPIO pin (params: `pin`) |
| `shelly.set` | Control Shelly relay (params: `host`, `state`, `label`) |
| `tempest.read` | Read latest Tempest weather observation |
| `flow.read` | Read TUF-2000M flow meter via Modbus RTU |

## Architecture

```
cmd/lastwatt/          CLI entry point (cobra)
internal/
  monitor/             Ping loop + state machine
  config/              YAML config loader + rate schedule generation
  engine/              Recipe executor
  scheduler/           Time-based schedule evaluation + flow override
  curtailment/         Coordination logic (ShouldRestore, vacation monitor)
  state/               JSON state persistence (debounced writes)
  actions/             Action interface + registry
    ecobee/            Ecobee thermostat (Auth0 web flow)
    gpio/              GPIO relay/LED (periph.io)
    shelly/            Shelly HTTP relay
    tempest/           Tempest weather station (UDP)
    flow/              TUF-2000M flow meter (Modbus RTU)
  forecast/            NWS hourly forecast provider
```

## Data Sources

The daemon runs several background data feeds:

- **Ping monitor** -- grid power status via ICMP to a device on grid power
- **Tempest UDP listener** -- real-time outdoor temperature, humidity, wind, solar radiation
- **NWS forecast** -- hourly forecast for the next 7 days
- **Flow meter** -- water flow rate via Modbus RTU (TUF-2000M)
- **Ecobee keepalive** -- proactive OAuth re-authentication + vacation mode polling

## License

MIT

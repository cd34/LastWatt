# LastWatt

A grid curtailment daemon for Raspberry Pi. Monitors grid power by pinging a device on the grid side (e.g., a Shelly plug). When the device stops responding, grid power is assumed lost and curtailment actions execute. When it returns, those actions are reversed.

## Features

- **Ping-based grid monitoring** with configurable fail/recover thresholds
- **Three curtailment modes** -- off-grid, time-of-use rate schedules, and vacation
- **Time-of-use rates** -- mid-peak and peak windows with timezone/DST support, weekends off-peak option
- **Flow override** -- temporarily restores the water heater when flow is detected during curtailment (configurable per action step)
- **Vacation mode** -- polls Ecobee for vacation events and curtails the water heater while away
- **Coordinated holds** -- water heater only restores when all holds (grid, schedule, vacation) are cleared
- **Modular action system** -- add new device types without changing the core
- **YAML recipes** with consistent `start`/`stop` action lists across all modes
- **Ecobee thermostat** control via web auth (no developer API key needed)
- **GPIO** relay and LED control via periph.io
- **Shelly** relay control via local HTTP API
- **Tempest weather station** integration (local UDP broadcast)
- **Flow meter** -- TUF-2000M ultrasonic flow meter via Modbus RTU
- **NWS hourly forecast** for weather-aware decision making
- **Schedule jitter** -- randomize start times to avoid simultaneous switching
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

LastWatt manages three independent reasons to curtail the water heater. The water heater only turns back on when **all** holds are cleared. Each mode uses consistent `start`/`stop` action lists.

### Off-Grid

When the monitored device stops responding (configurable fail threshold), `grid.start` runs -- sets the thermostat to emergency temps, turns off the water heater, lights the curtail LED. When grid power returns, `grid.stop` reverses everything. If a rate schedule or vacation hold is still active, the water heater stays off.

Actions marked with `flow_override: true` are temporarily reversed when water flow is detected (someone is showering). They re-engage when flow stops.

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
  start:
    - action: gpio.set
      params: { pin: "17", state: off, label: water_heater }
      flow_override: true
  stop:
    - action: gpio.set
      params: { pin: "17", state: on, label: water_heater }
      flow_override: true
```

Actions with `flow_override: true` are temporarily reversed when water flow is detected during a rate window. They re-engage when flow stops.

### Vacation

Polls the Ecobee API for active vacation events. When vacation mode is detected, the water heater turns off. When vacation ends, the water heater only restores if the grid is up and no rate schedule is active.

Omit `flow_override` on vacation actions -- no reason to heat water in an empty house. Add `flow_override: true` if someone is still present (pet sitter, house guest).

```yaml
vacation:
  poll_interval: 10m
  start:
    - action: gpio.set
      params: { pin: "17", state: off, label: water_heater }
  stop:
    - action: gpio.set
      params: { pin: "17", state: on, label: water_heater }
```

### Triggers

Condition-based rules that watch store values and fire `start`/`stop` actions on transitions. All conditions in `when` must be true (AND). Evaluated every 30 seconds.

```yaml
triggers:
  - name: heat_warning
    when:
      - "tempest.temp_f > 90"
      - "ecobee.saved_mode == heat"
    start:
      - action: gpio.set
        params: { pin: "22", state: on, label: heat_warning_led }
    stop:
      - action: gpio.set
        params: { pin: "22", state: off, label: heat_warning_led }
    respect_holds: false     # run stop even if grid/schedule/vacation hold active
```

Operators: `==`, `!=`, `>`, `<`, `>=`, `<=`. Numeric values are compared numerically; strings are compared lexicographically. If a store key doesn't exist yet, the condition evaluates to false.

Available store keys include `tempest.temp_f`, `tempest.humidity`, `tempest.wind_mph`, `tempest.solar_rad`, `ecobee.saved_mode`, `ecobee.vacation_active`, `flow.flowing`, `flow.rate`, `schedule.active`, and `trigger.<name>`.

### Interaction Matrix

`flow_override` is set per action step, not per mode. Only the specific devices marked with `flow_override: true` respond to flow detection -- other curtailed devices stay off. This lets you curtail a water heater and a pool pump during peak hours, but only restore the water heater when someone turns on a faucet.

| Event | Grid | Vacation | Rate Schedule | Water Heater | Flow Override |
|---|---|---|---|---|---|
| Normal operation | up | off | none | ON | n/a |
| Grid goes down | **down** | off | none | OFF | per step flag |
| Flow detected, grid down | down | off | none | **ON** (temp) | active (if flagged) |
| Flow stops, grid down | down | off | none | OFF | deactivated |
| Rate window enters | up | off | **active** | OFF | per step flag |
| Flow detected, rate active | up | off | active | **ON** (temp) | active (if flagged) |
| Vacation starts | up | **on** | none | OFF | per step flag |
| Grid restores, vacation active | up | on | none | OFF | -- |
| Grid restores, rate active | up | off | active | OFF | per step flag |
| Vacation ends, rate active | up | off | active | OFF | per step flag |
| Rate ends, vacation active | up | on | none | OFF | per step flag |
| Mid-peak ends, peak active | up | off | active | OFF | per step flag |
| All holds clear | up | off | none | ON | n/a |

## CLI

```
lastwatt daemon              # run the monitor loop
lastwatt status              # show current state (normal/curtailed)
lastwatt run start           # manually trigger grid start recipe
lastwatt run stop            # manually trigger grid stop recipe
lastwatt action <name> [-p key=value ...]  # run a single action
lastwatt ecobee-auth         # authenticate with Ecobee
lastwatt forecast            # show NWS hourly forecast
```

## Config

See [config.yaml.sample](config.yaml.sample) for a full example with comments. The grid monitor config lives under `grid:`. All modes use `start`/`stop` action lists:

```yaml
grid:
  monitor:
    host: 192.168.1.X
    interval: 5s
    fail_threshold: 3
    recover_threshold: 2
  start:
    - action: gpio.set
      params: { pin: "17", state: off, label: water_heater }
      flow_override: true    # this device responds to flow detection
    - action: gpio.set
      params: { pin: "27", state: on, label: curtail_led }
  stop: [...]

rates:
  timezone: America/Denver
  weekends_offpeak: true
  peak: { start: "17:00", end: "21:00" }
  mid_peak: { start: "13:00", end: "17:00" }
  start:
    - action: gpio.set
      params: { pin: "17", state: off, label: water_heater }
      flow_override: true
  stop: [...]

vacation:
  poll_interval: 10m
  start:
    - action: gpio.set
      params: { pin: "17", state: off, label: water_heater }
      # no flow_override — nobody home
  stop: [...]
```

The `flow_meter` section defines the connection to the flow sensor:

```yaml
flow_meter:
  port: /dev/ttyUSB0
  baud: 9600
  slave_id: 1
  interval: 5s
```

Custom schedules use `begin`/`end` for the time window and `start`/`stop` for actions:

```yaml
schedules:
  - name: evening_lights
    days: [Mon, Tue, Wed, Thu, Fri]
    begin: "22:00"
    end: "06:00"
    jitter: 10m
    start:
      - action: shelly.set
        params: { host: 192.168.1.X, state: off, label: porch_light }
    stop:
      - action: shelly.set
        params: { host: 192.168.1.X, state: on, label: porch_light }
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

## Hardware

| Device | Purpose |
|---|---|
| Raspberry Pi | Runs the daemon, GPIO for relays/LEDs |
| [Shelly 1 Mini Gen3](https://us.shelly.com/products/shelly-1-mini-gen3) | Network relay, switches contactor coil for water heater via local HTTP API |
| [Shelly PM Mini Gen3](https://us.shelly.com/products/shelly-pm-mini-gen3) | Power monitoring without relay |
| 2-pole 30A/240V contactor (120V coil) | Switches water heater power (e.g., Packard C230B) |
| [TUF-2000M ultrasonic flow meter](https://www.aliexpress.us/item/3256808444609453.html) | Clamp-on flow detection for flow_override via Modbus RTU |
| [Olimex USB-RS485](https://www.digikey.com/en/products/detail/olimex-ltd/USB-RS485/21661988) | Connects Pi to TUF-2000M via Modbus RTU |
| [Ecobee thermostat](https://www.ecobee.com/) | Temperature holds during curtailment, vacation mode detection |
| [WeatherFlow Tempest](https://weatherflow.com/tempest-weather-system/) | Outdoor weather data via local UDP |

## License

MIT

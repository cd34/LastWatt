# TUF-2000M Ultrasonic Flowmeter – Quick Wiring & Power Reference

## Power Supply
* **Range:** DC 8 ~ 36 V, ≤1.5 W / ≈50 mA  
  *Also acceptable:* 10 ~ 36 VAC
* Terminals: `8~36V+`, `8~36V-`
* Do NOT apply 110/220 VAC – will damage module.
* Safety note from manual: Power supply must be within DC 8~36 V range; avoid AC mains.

## Rear Interface / Terminal Block

> NOTE: The terminal pin-outs may vary with different module.

### Power & Communication
* `8~36V+, 8~36V-` – Power supply. 10~36 VAC also applicable.
* `485+, 485-` – RS485 terminals, isolated and surge protected.
  * Default serial: 9600 baud, none parity, 8 data bits, 1 stop bit.
  * Menu M62 for RS232/RS485 setup; MODBUS-RTU supported.

### Analog I/O
* `AO+, AO-` – Analog output, loop powered 4~20 mA.
* `AI3, AI4, AI5` – Analog inputs. Ground connects to GND.

### Transducer Connections
* `UP+, UP-` – Connect to the upstream transducer
* `DN+, DN-` – Connect to the downstream transducer
* `GND` – ‘Ground’ for the transducers. **NOT** for power and AO and RS485.  
  Avoid any connection from power to the GND terminal, or isolation will be lost.

### Temperature / Heat
* `T1, T2` – Connect to signal terminals of the PT100 RTD
* `TX1, TX2` – Connect to power terminals of the PT100 RTD
  * Return terminals of the RTD connect to ‘GND’

### Outputs
* `OCT+, OCT-` – OCT output terminals. Related to Menu78.
* `OCT2+, OCT2-` – OCT outputs, related to MENU79 / RELAY output setup.

## Transducer Wiring – Clamp-on Type
Section 5.3 Transducers Installation → 5.3.1 Wiring diagram of transducer

* Upstream clamp-on transducer → UP+/UP-
* Downstream clamp-on transducer → DN+/DN-
* Common transducer ground → GND

The module accepts any transducer type made by the manufacturer, including clamp-on, insertion, PI-type and standard-pipe types. Transducer mounting method selectable in Menu M24:
0 V-method, 1 Z-method, 2 N-method, 3 W-method.

## Installation Notes
* Power wiring: Connect DC 8~36 V to POWER interface with correct polarity.
* Keep communication cables ≥20 cm away from high-voltage cables to avoid interference.
* For heat measurement, connect PT100 temperature sensors on supply and return pipes to T1/T2 and TX1/TX2.

## Sources
* ManualsLib – Tsonic TUF-2000M User Manual `3858897` pages 6 Converter Installation And Wiring Diagram; 11 Transducer Introduction And Wiring Diagram.
* TUF-2000 Series User Manual `1628951`.
* ValueStore.us TUF-2000M.pdf technical manual v13.44+ – Terminal description p.7-8, Power spec p.2-3.

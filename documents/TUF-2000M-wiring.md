# TUF-2000M Ultrasonic Flowmeter – Quick Wiring & Power Reference

## Power Supply
* **Range:** DC 8 ~ 36 V, ≤1.5 W / ≈50 mA  
  *Also acceptable:* 10 ~ 36 VAC
* Terminals: `8~36V+`, `8~36V-`
* Do NOT apply 110/220 VAC – will damage module.
* Safety note from manual: Power supply must be within DC 8~36 V range; avoid AC mains.
* Recommended supply: 12 VDC with bare leads is within spec. Use inline fuse 0.5-1 A at supply.
* Power lead gauge: 18 AWG stranded minimum for 12 V, 16 AWG preferred for runs >10 m to limit voltage drop.

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

Wire count: 2 wires per transducer + common GND = 3 conductors per head in practice.

Cable recommendation:
* Use shielded twisted pair 18-22 AWG stranded per transducer. 20 AWG stranded is practical for runs up to ~100 m.
* Stranded conductor preferred over solid for field wiring and screw terminals.
* If you have 4-wire cable, use one pair for UP+ / UP-, one pair for DN+ / DN-. Tie spare conductors together to GND at module end, or run a 3-conductor shielded cable to each head and use shield as GND.

Note: Transducer signal current is very low; gauge is chosen for mechanical durability and noise immunity.

The module accepts any transducer type made by the manufacturer, including clamp-on, insertion, PI-type and standard-pipe types. Transducer mounting method selectable in Menu M24:
0 V-method, 1 Z-method, 2 N-method, 3 W-method.

## Installation Notes
* Power wiring: Connect DC 8~36 V to POWER interface with correct polarity.
* Keep communication cables ≥20 cm away from high-voltage cables to avoid interference.
* For heat measurement, connect PT100 temperature sensors on supply and return pipes to T1/T2 and TX1/TX2.

## Sources & References
* ManualsLib – Tsonic TUF-2000M User Manual `3858897` pages 6 Converter Installation And Wiring Diagram; 11 Transducer Introduction And Wiring Diagram.
  * https://www.manualslib.com/manual/3858897/Tsonic-Tuf-2000m.html?page=6
  * https://www.manualslib.com/manual/3858897/Tsonic-Tuf-2000m.html?page=11
* TUF-2000 Series User Manual `1628951`.
  * https://www.manualslib.com/manual/1628951/Tsonic-Tuf-2000-Series.html?page=11
* ValueStore.us TUF-2000M.pdf technical manual v13.44+ – Terminal description p.7-8, Power spec p.2-3.
  * https://valuestore.us/wp-content/uploads/2018/11/TUF-2000M.pdf
* AliExpress PDF excerpt – Power supply must be within DC 8~36V range.
  * https://ae01.alicdn.com/kf/Sff702632eae14199a0599c54cf6d3d0du.pdf
* Amazon technical manual reference for TUF-2000M wiring diagram.
  * https://images-na.ssl-images-amazon.com/images/I/91CvZHsNYBL.pdf

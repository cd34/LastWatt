// Package sun computes whether the sun is up at the daemon's configured
// location and writes sun.is_day / sun.elevation to the state store.
package sun

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/mcd/lastwatt/internal/actions"
)

// refraction is the apparent altitude at which the sun's upper limb touches
// the horizon, accounting for atmospheric bending. The sun is "up" when
// elevation exceeds this value.
const refraction = -0.833

// Provider periodically updates sun.is_day in the state store.
type Provider struct {
	lat, lon float64
	store    actions.StateStore
	log      *slog.Logger
}

func NewProvider(lat, lon float64, store actions.StateStore, log *slog.Logger) *Provider {
	return &Provider{lat: lat, lon: lon, store: store, log: log}
}

// Run ticks every minute, updating sun.is_day and sun.elevation in the store.
// Blocks until ctx is cancelled.
func (p *Provider) Run(ctx context.Context) error {
	p.update(time.Now())
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case t := <-ticker.C:
			p.update(t)
		}
	}
}

func (p *Provider) update(now time.Time) {
	elev := SolarElevation(now, p.lat, p.lon)
	isDay := elev > refraction
	p.store.Set("sun.is_day", fmt.Sprintf("%t", isDay))
	p.store.Set("sun.elevation", fmt.Sprintf("%.1f", elev))
}

// SolarElevation returns the sun's apparent altitude in degrees above the
// horizon at the given time and location. Implements NOAA's general solar
// position algorithm; accurate to within a few minutes at sunrise/sunset.
func SolarElevation(t time.Time, lat, lon float64) float64 {
	utc := t.UTC()
	jd := julianDay(utc)
	T := (jd - 2451545.0) / 36525.0

	L0 := math.Mod(280.46646+T*(36000.76983+T*0.0003032), 360.0)
	M := 357.52911 + T*(35999.05029-T*0.0001537)
	e := 0.016708634 - T*(0.000042037+T*0.0000001267)

	Mr := deg2rad(M)
	C := math.Sin(Mr)*(1.914602-T*(0.004817+T*0.000014)) +
		math.Sin(2*Mr)*(0.019993-T*0.000101) +
		math.Sin(3*Mr)*0.000289

	lambda := L0 + C
	omega := 125.04 - 1934.136*T
	appLambda := lambda - 0.00569 - 0.00478*math.Sin(deg2rad(omega))

	eps0 := 23.0 + (26.0+(21.448-T*(46.815+T*(0.00059-T*0.001813)))/60.0)/60.0
	eps := eps0 + 0.00256*math.Cos(deg2rad(omega))

	decl := rad2deg(math.Asin(math.Sin(deg2rad(eps)) * math.Sin(deg2rad(appLambda))))

	y := math.Tan(deg2rad(eps / 2))
	y = y * y
	eotRad := y*math.Sin(2*deg2rad(L0)) -
		2*e*math.Sin(Mr) +
		4*e*y*math.Sin(Mr)*math.Cos(2*deg2rad(L0)) -
		0.5*y*y*math.Sin(4*deg2rad(L0)) -
		1.25*e*e*math.Sin(2*Mr)
	EOT := 4 * rad2deg(eotRad) // minutes

	minutesUTC := float64(utc.Hour()*60+utc.Minute()) + float64(utc.Second())/60.0
	trueSolarTime := math.Mod(minutesUTC+EOT+4*lon, 1440.0)
	if trueSolarTime < 0 {
		trueSolarTime += 1440.0
	}
	HA := trueSolarTime/4.0 - 180.0

	latR := deg2rad(lat)
	declR := deg2rad(decl)
	haR := deg2rad(HA)
	sinElev := math.Sin(latR)*math.Sin(declR) + math.Cos(latR)*math.Cos(declR)*math.Cos(haR)
	return rad2deg(math.Asin(sinElev))
}

func julianDay(t time.Time) float64 {
	y, m, d := t.Year(), int(t.Month()), t.Day()
	if m <= 2 {
		y -= 1
		m += 12
	}
	A := y / 100
	B := 2 - A + A/4
	jd := math.Floor(365.25*float64(y+4716)) +
		math.Floor(30.6001*float64(m+1)) +
		float64(d) + float64(B) - 1524.5
	frac := (float64(t.Hour())*3600 + float64(t.Minute())*60 + float64(t.Second())) / 86400.0
	return jd + frac
}

func deg2rad(d float64) float64 { return d * math.Pi / 180 }
func rad2deg(r float64) float64 { return r * 180 / math.Pi }

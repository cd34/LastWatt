package sun

import (
	"testing"
	"time"
)

// TestSolarElevation_Noon checks that the sun is high in the sky at solar noon
// on the summer solstice in the northern hemisphere.
func TestSolarElevation_NoonSummerSolstice(t *testing.T) {
	// Boulder, CO at ~12:00 local (18:00 UTC, MDT) on summer solstice
	when := time.Date(2025, 6, 21, 19, 0, 0, 0, time.UTC) // ~13:00 MDT (solar noon offset)
	elev := SolarElevation(when, 40.0150, -105.2705)
	if elev < 60 {
		t.Errorf("expected elev > 60° near solar noon on solstice, got %.1f°", elev)
	}
}

// TestSolarElevation_Midnight verifies the sun is below the horizon at
// local midnight regardless of season.
func TestSolarElevation_Midnight(t *testing.T) {
	// Boulder, CO at ~00:00 local (06:00 UTC) — sun should be well below horizon
	when := time.Date(2025, 3, 15, 6, 0, 0, 0, time.UTC)
	elev := SolarElevation(when, 40.0150, -105.2705)
	if elev > -10 {
		t.Errorf("expected elev well below horizon at midnight, got %.1f°", elev)
	}
}

// TestSolarElevation_CrossesHorizon verifies the sun is up some time after
// sunrise and down some time after sunset.
func TestSolarElevation_DiurnalCycle(t *testing.T) {
	lat, lon := 40.0150, -105.2705
	// Boulder approx sunrise ~5:30 MDT in June (11:30 UTC)
	dawn := time.Date(2025, 6, 21, 8, 0, 0, 0, time.UTC) // 02:00 MDT — pre-dawn
	noon := time.Date(2025, 6, 21, 19, 0, 0, 0, time.UTC)
	dusk := time.Date(2025, 6, 22, 5, 0, 0, 0, time.UTC) // 23:00 MDT — post-sunset

	if SolarElevation(dawn, lat, lon) > 0 {
		t.Error("sun should be below horizon at 2 AM local")
	}
	if SolarElevation(noon, lat, lon) < 60 {
		t.Error("sun should be high at solar noon")
	}
	if SolarElevation(dusk, lat, lon) > 0 {
		t.Error("sun should be below horizon at 11 PM local")
	}
}

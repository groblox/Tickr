package astro

import (
	"testing"
	"time"
)

func within(t *testing.T, name string, got time.Time, want time.Time, tol time.Duration) {
	t.Helper()
	d := got.Sub(want)
	if d < 0 {
		d = -d
	}
	if d > tol {
		t.Errorf("%s: got %v want %v (off by %v)", name, got, want, d)
	}
}

func TestSeasons(t *testing.T) {
	// Reference times from the US Naval Observatory (UTC).
	within(t, "March equinox 2024", SeasonTime(2024, MarchEquinox), time.Date(2024, 3, 20, 3, 6, 0, 0, time.UTC), 10*time.Minute)
	within(t, "June solstice 2024", SeasonTime(2024, JuneSolstice), time.Date(2024, 6, 20, 20, 51, 0, 0, time.UTC), 10*time.Minute)
	within(t, "September equinox 2026", SeasonTime(2026, SeptemberEquinox), time.Date(2026, 9, 23, 0, 5, 0, 0, time.UTC), 10*time.Minute)
	within(t, "December solstice 2024", SeasonTime(2024, DecemberSolstice), time.Date(2024, 12, 21, 9, 20, 0, 0, time.UTC), 10*time.Minute)
	evs := Seasons(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
	if len(evs) != 2 || evs[0].Season != SeptemberEquinox || evs[1].Season != DecemberSolstice {
		t.Fatalf("expected equinox+solstice, got %+v", evs)
	}
}

func TestMoonPhases(t *testing.T) {
	// Reference: new moon 2024-01-11 11:57 UTC, full moon 2024-01-25 17:54 UTC.
	evs := MoonPhases(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC))
	if len(evs) != 2 {
		t.Fatalf("expected 2 phases in Jan 2024, got %d: %+v", len(evs), evs)
	}
	within(t, "new moon", evs[0].Time, time.Date(2024, 1, 11, 11, 57, 0, 0, time.UTC), 15*time.Minute)
	within(t, "full moon", evs[1].Time, time.Date(2024, 1, 25, 17, 54, 0, 0, time.UTC), 15*time.Minute)
	if evs[0].Phase != NewMoon || evs[1].Phase != FullMoon {
		t.Fatalf("phase order wrong: %+v", evs)
	}
	// Full moon 2026-09-26 16:49 UTC (Harvest Moon).
	evs = MoonPhases(time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if len(evs) != 1 || evs[0].Phase != FullMoon {
		t.Fatalf("expected one full moon late Sep 2026, got %+v", evs)
	}
	within(t, "harvest moon 2026", evs[0].Time, time.Date(2026, 9, 26, 16, 49, 0, 0, time.UTC), 30*time.Minute)
	if FullMoonName(evs[0].Time.Month()) != "Harvest Moon" {
		t.Fatal("expected Harvest Moon")
	}
}

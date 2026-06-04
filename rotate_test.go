package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustDate(s string) time.Time {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestSelectTier(t *testing.T) {
	cases := []struct {
		date string
		dow  int
		want tier
	}{
		{"2026-06-01", 7, tierMonthly}, // 1st always monthly
		{"2026-06-07", 7, tierWeekly},  // Sunday, dow=7
		{"2026-06-04", 7, tierDaily},   // Thursday
		{"2026-06-08", 1, tierWeekly},  // Monday, dow=1
	}
	for _, c := range cases {
		got := selectTier(mustDate(c.date), c.dow)
		if got != c.want {
			t.Errorf("selectTier(%s, dow=%d) = %s, want %s", c.date, c.dow, got, c.want)
		}
	}
}

func TestCutoffForMonthlyKeepForever(t *testing.T) {
	cfg := defaultConfig() // MonthsToKeep = 0
	if c := cutoffFor(mustDate("2026-06-01"), tierMonthly, cfg); !c.IsZero() {
		t.Errorf("monthly cutoff with months_to_keep=0 should be zero (keep forever), got %v", c)
	}
}

func TestRotatePrunesOnlyExpiredOfTier(t *testing.T) {
	root := t.TempDir()
	dirs := []string{
		"2026-05-01-daily",   // old daily -> delete (cutoff keeps 7 days)
		"2026-06-03-daily",   // recent daily -> keep
		"2026-05-01-weekly",  // weekly -> untouched by daily rotation
		"2026-05-01-monthly", // monthly -> untouched
		"random-folder",      // ignored
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}

	now := mustDate("2026-06-04")
	cfg := defaultConfig() // DaysToKeep = 7
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := rotate(root, tierDaily, cutoffFor(now, tierDaily, cfg), false, log); err != nil {
		t.Fatal(err)
	}

	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(root, name))
		return err == nil
	}
	if exists("2026-05-01-daily") {
		t.Error("expired daily backup should have been deleted")
	}
	for _, keep := range []string{"2026-06-03-daily", "2026-05-01-weekly", "2026-05-01-monthly", "random-folder"} {
		if !exists(keep) {
			t.Errorf("%s should have been kept", keep)
		}
	}
}

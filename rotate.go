package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tier is one rotation class. Directories are named "<YYYY-MM-DD>-<tier>".
type tier string

const (
	tierDaily   tier = "daily"
	tierWeekly  tier = "weekly"
	tierMonthly tier = "monthly"

	dateLayout = "2006-01-02"
)

// selectTier reproduces the bash schedule: the 1st of the month is monthly,
// the configured weekday is weekly, everything else is daily.
func selectTier(now time.Time, dayOfWeekToKeep int) tier {
	if now.Day() == 1 {
		return tierMonthly
	}
	if isoWeekday(now) == dayOfWeekToKeep {
		return tierWeekly
	}
	return tierDaily
}

// isoWeekday returns 1-7 for Mon-Sun (Go's Weekday is 0=Sun..6=Sat).
func isoWeekday(t time.Time) int {
	if t.Weekday() == time.Sunday {
		return 7
	}
	return int(t.Weekday())
}

// dirName is the dated directory for a tier, e.g. "2026-06-04-daily".
func dirName(now time.Time, t tier) string {
	return fmt.Sprintf("%s-%s", now.Format(dateLayout), t)
}

// rotate deletes expired directories of the given tier by parsing the date
// embedded in each directory name, which is far more robust than find -mtime.
// cutoff is the oldest date to keep; anything strictly before it is removed.
// A zero cutoff means "keep everything" (no pruning).
func rotate(root string, t tier, cutoff time.Time, dryRun bool, log *slog.Logger) error {
	if cutoff.IsZero() {
		log.Info("rotation disabled for tier (keep forever)", "tier", t)
		return nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to rotate yet
		}
		return fmt.Errorf("read backup dir %q: %w", root, err)
	}

	suffix := "-" + string(t)
	var removed, errs int
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), suffix) {
			continue
		}
		datePart := strings.TrimSuffix(e.Name(), suffix)
		d, err := time.Parse(dateLayout, datePart)
		if err != nil {
			log.Warn("skipping unparseable backup dir", "name", e.Name())
			continue
		}
		if !d.Before(cutoff) {
			continue // still within retention
		}

		full := filepath.Join(root, e.Name())
		if dryRun {
			log.Info("dry-run: would delete expired backup", "dir", full)
			removed++
			continue
		}
		if err := os.RemoveAll(full); err != nil {
			log.Error("failed to delete expired backup", "dir", full, "err", err)
			errs++
			continue
		}
		log.Info("deleted expired backup", "dir", full)
		removed++
	}

	log.Info("rotation complete", "tier", t, "removed", removed)
	if errs > 0 {
		return fmt.Errorf("rotation: %d directory removal(s) failed", errs)
	}
	return nil
}

// cutoffFor computes the retention cutoff date for a tier relative to now.
// Returns the zero time when retention is set to "keep forever".
func cutoffFor(now time.Time, t tier, cfg Config) time.Time {
	switch t {
	case tierDaily:
		return now.AddDate(0, 0, -cfg.DaysToKeep)
	case tierWeekly:
		return now.AddDate(0, 0, -7*cfg.WeeksToKeep)
	case tierMonthly:
		if cfg.MonthsToKeep <= 0 {
			return time.Time{} // keep forever
		}
		return now.AddDate(0, -cfg.MonthsToKeep, 0)
	default:
		return time.Time{}
	}
}

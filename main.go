// Command pgbackup is a drop-in replacement for the classic
// pg_backup_rotated.sh / pg_backup.sh shell scripts, as a single static binary.
// It shells out to pg_dump / pg_dumpall / psql (the standard postgresql-client
// tools) and handles gzip compression in-process. Its only library dependency is
// a YAML parser for the config file.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"syscall"
	"time"
)

// Version and BuildTime are injected at build time via -ldflags -X.
var (
	Version   = "dev"
	BuildTime = "unknown"
)

type options struct {
	configPath string
	tierFlag   string
	dateFlag   string
	logDirFlag string
	logDirSet  bool // whether -log-dir was passed (overrides config)
	dryRun     bool
	jsonLog    bool
}

func main() {
	var (
		o           options
		showVersion bool
	)
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.StringVar(&o.configPath, "config", "pg_backup.yaml", "path to YAML config file")
	flag.StringVar(&o.tierFlag, "tier", "auto", "tier to run: auto|daily|weekly|monthly")
	flag.BoolVar(&o.dryRun, "dry-run", false, "log actions without writing or deleting anything")
	flag.BoolVar(&o.jsonLog, "json", false, "emit logs as JSON instead of text")
	flag.StringVar(&o.dateFlag, "date", "", "override 'today' as YYYY-MM-DD (testing)")
	flag.StringVar(&o.logDirFlag, "log-dir", "", "directory for log files (overrides config; '-' = stderr only)")
	flag.Parse()

	if showVersion {
		fmt.Printf("pgbackup %s (built %s)\n", Version, BuildTime)
		return
	}

	flag.Visit(func(f *flag.Flag) {
		if f.Name == "log-dir" {
			o.logDirSet = true
		}
	})

	// Bootstrap logger for failures that happen before the config (and thus the
	// real log destination) is known.
	boot := newLogger(os.Stderr, slog.LevelInfo, o.jsonLog)

	if err := run(o, boot); err != nil {
		os.Exit(1)
	}
}

func run(o options, boot *slog.Logger) error {
	cfg, err := loadConfig(o.configPath)
	if err != nil {
		boot.Error("config error", "err", err)
		return err
	}

	logDir := cfg.LogDir
	if o.logDirSet {
		logDir = o.logDirFlag
	}
	log, closeLog := setupLogger(logDir, o.jsonLog, time.Now())
	defer func() { _ = closeLog() }()

	if err := backup(o, cfg, log); err != nil {
		log.Error("backup run failed", "err", err)
		return err
	}
	return nil
}

func backup(o options, cfg Config, log *slog.Logger) error {
	if err := ensureBackupUser(cfg.BackupUser); err != nil {
		return err
	}

	now := time.Now()
	if o.dateFlag != "" {
		var err error
		now, err = time.Parse(dateLayout, o.dateFlag)
		if err != nil {
			return fmt.Errorf("invalid -date %q: %w", o.dateFlag, err)
		}
	}

	t, err := resolveTier(o.tierFlag, now, cfg.DayOfWeekToKeep)
	if err != nil {
		return err
	}

	// Set up a cancellable context: SIGINT/SIGTERM aborts in-flight dumps,
	// and an optional config timeout bounds the whole run.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if d, err := cfg.timeout(); err != nil {
		return fmt.Errorf("invalid timeout: %w", err)
	} else if d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}

	log.Info("starting backup",
		"tier", t, "date", now.Format(dateLayout),
		"host", cfg.Host, "port", cfg.Port, "backup_dir", cfg.BackupDir, "dry_run", o.dryRun)

	// Prune expired backups of this tier first (matches the bash order).
	if err := rotate(cfg.BackupDir, t, cutoffFor(now, t, cfg), o.dryRun, log); err != nil {
		// A rotation failure is worth surfacing but shouldn't block new backups.
		log.Error("rotation reported errors (continuing to backup)", "err", err)
	}

	eng := engine{cfg: cfg, log: log, dryRun: o.dryRun}
	dir := filepath.Join(cfg.BackupDir, dirName(now, t))
	return eng.performBackups(ctx, dir)
}

func resolveTier(flagVal string, now time.Time, dayOfWeekToKeep int) (tier, error) {
	switch flagVal {
	case "", "auto":
		return selectTier(now, dayOfWeekToKeep), nil
	case "daily":
		return tierDaily, nil
	case "weekly":
		return tierWeekly, nil
	case "monthly":
		return tierMonthly, nil
	default:
		return "", fmt.Errorf("unknown -tier %q (want auto|daily|weekly|monthly)", flagVal)
	}
}

// ensureBackupUser aborts unless the process runs as the configured OS user.
// An empty BackupUser skips the check, exactly like the shell script.
func ensureBackupUser(want string) error {
	if want == "" {
		return nil
	}
	u, err := user.Current()
	if err != nil {
		return fmt.Errorf("determine current user: %w", err)
	}
	if u.Username != want {
		return fmt.Errorf("must run as %q but running as %q", want, u.Username)
	}
	return nil
}

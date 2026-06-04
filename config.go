package main

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config mirrors pg_backup.config but typed and validated.
// Password is NOT stored here: use PGPASSWORD env or ~/.pgpass, like libpq.
type Config struct {
	Host     string `yaml:"host"`     // default "localhost"
	Port     int    `yaml:"port"`     // default 5432
	Username string `yaml:"username"` // default "postgres"
	Database string `yaml:"database"` // maintenance DB to connect to for listing; default "postgres"

	// BackupUser: if set, the process must run as this OS user, else it aborts.
	BackupUser string `yaml:"backup_user"`

	// BackupDir is the root under which dated tier directories are created.
	BackupDir string `yaml:"backup_dir"`

	// LogDir is where dated log files are written. Empty or "-" => stderr only.
	// Defaults to /var/log/pgbackup. A -log-dir flag overrides this.
	LogDir string `yaml:"log_dir"`

	// SchemaOnlyList: databases matching any of these regexes get a schema-only
	// dump and are excluded from full dumps (same semantics as the bash script).
	SchemaOnlyList []string `yaml:"schema_only_list"`

	EnableGlobalsBackups bool `yaml:"enable_globals_backups"`
	EnablePlainBackups   bool `yaml:"enable_plain_backups"`
	EnableCustomBackups  bool `yaml:"enable_custom_backups"`

	// IncludeCreateDatabase adds pg_dump --create to full backups, so each dump
	// recreates the database (owner, encoding, locale, DB-level grants) on
	// restore instead of needing it to exist first. Does not affect schema-only
	// dumps. Default true.
	IncludeCreateDatabase bool `yaml:"include_create_database"`

	// Rotation retention.
	DayOfWeekToKeep int `yaml:"day_of_week_to_keep"` // 1-7 = Mon-Sun
	DaysToKeep      int `yaml:"days_to_keep"`
	WeeksToKeep     int `yaml:"weeks_to_keep"`
	MonthsToKeep    int `yaml:"months_to_keep"` // 0 = keep forever (never prune monthly)

	// Optional overall deadline for a single run. "0" / "" disables it.
	Timeout string `yaml:"timeout"`
}

func defaultConfig() Config {
	return Config{
		Host:                  "localhost",
		Port:                  5432,
		Username:              "postgres",
		Database:              "postgres",
		LogDir:                defaultLogDir,
		EnableGlobalsBackups:  true,
		EnablePlainBackups:    true,
		EnableCustomBackups:   false,
		IncludeCreateDatabase: true,
		DayOfWeekToKeep:       7,
		DaysToKeep:            7,
		WeeksToKeep:           4,
		MonthsToKeep:          0,
	}
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config %q: %w", path, err)
	}

	// Decode onto the defaults so omitted keys keep their default values.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("parse config %q: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.BackupDir == "" {
		return fmt.Errorf("backup_dir is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("port %d out of range", c.Port)
	}
	if c.DayOfWeekToKeep < 1 || c.DayOfWeekToKeep > 7 {
		return fmt.Errorf("day_of_week_to_keep must be 1-7, got %d", c.DayOfWeekToKeep)
	}
	// Guard against negative retention: it would push the rotation cutoff into
	// the future and delete every backup of that tier.
	if c.DaysToKeep < 0 || c.WeeksToKeep < 0 || c.MonthsToKeep < 0 {
		return fmt.Errorf("retention values must be >= 0 (days=%d weeks=%d months=%d)",
			c.DaysToKeep, c.WeeksToKeep, c.MonthsToKeep)
	}
	if !c.EnablePlainBackups && !c.EnableCustomBackups {
		return fmt.Errorf("at least one of enable_plain_backups / enable_custom_backups must be true")
	}
	return nil
}

func (c Config) timeout() (time.Duration, error) {
	if c.Timeout == "" || c.Timeout == "0" {
		return 0, nil
	}
	return time.ParseDuration(c.Timeout)
}

package main

import "testing"

func baseValidCfg() Config {
	c := defaultConfig()
	c.BackupDir = "/var/lib/backup/db"
	return c
}

func TestValidateRejectsNegativeRetention(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
	}{
		{"days", func(c *Config) { c.DaysToKeep = -1 }},
		{"weeks", func(c *Config) { c.WeeksToKeep = -1 }},
		{"months", func(c *Config) { c.MonthsToKeep = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := baseValidCfg()
			tc.mut(&c)
			if err := c.validate(); err == nil {
				t.Errorf("expected validation error for negative %s retention", tc.name)
			}
		})
	}
}

func TestValidateAcceptsDefaults(t *testing.T) {
	if err := baseValidCfg().validate(); err != nil {
		t.Errorf("default config should validate, got: %v", err)
	}
}

func TestValidateRequiresBackupDir(t *testing.T) {
	c := defaultConfig() // BackupDir empty
	if err := c.validate(); err == nil {
		t.Error("expected error when backup_dir is empty")
	}
}

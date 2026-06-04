package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// engine performs the actual dumps for one tier into one dated directory.
type engine struct {
	cfg    Config
	log    *slog.Logger
	dryRun bool
}

// baseArgs are the connection flags shared by every pg client invocation.
func (e engine) baseArgs() []string {
	return []string{
		"-h", e.cfg.Host,
		"-p", fmt.Sprintf("%d", e.cfg.Port),
		"-U", e.cfg.Username,
	}
}

// createFlag returns the pg_dump --create flag when database (re)creation is
// requested, so full dumps are self-restoring. Empty otherwise.
func (e engine) createFlag() []string {
	if e.cfg.IncludeCreateDatabase {
		return []string{"--create"}
	}
	return nil
}

// performBackups reproduces the bash perform_backups() for a single tier.
func (e engine) performBackups(ctx context.Context, dir string) error {
	e.log.Info("creating backup directory", "dir", dir)
	if !e.dryRun {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("cannot create backup directory %q: %w", dir, err)
		}
	}

	var failures int

	// 1. Globals (roles, tablespaces) — pg_dumpall -g.
	if e.cfg.EnableGlobalsBackups {
		e.log.Info("globals backup")
		out := filepath.Join(dir, "globals.sql.gz")
		if err := e.dumpToGzip(ctx, out, "pg_dumpall", append(e.baseArgs(), "-g")...); err != nil {
			e.log.Error("failed to produce globals backup", "err", err)
			failures++
		}
	} else {
		e.log.Info("globals backup disabled")
	}

	// 2. Schema-only databases.
	schemaDBs, err := e.listDatabases(ctx, e.schemaOnlyQuery())
	if err != nil {
		return fmt.Errorf("listing schema-only databases: %w", err)
	}
	if len(schemaDBs) > 0 {
		e.log.Info("schema-only databases matched", "databases", schemaDBs)
	}
	for _, db := range schemaDBs {
		e.log.Info("schema-only backup", "database", db)
		out := filepath.Join(dir, db+"_SCHEMA.sql.gz")
		args := append(e.baseArgs(), "-Fp", "-s", db)
		if err := e.dumpToGzip(ctx, out, "pg_dump", args...); err != nil {
			e.log.Error("failed to backup database schema", "database", db, "err", err)
			failures++
		}
	}

	// 3. Full backups (plain and/or custom) of everything else.
	fullDBs, err := e.listDatabases(ctx, e.fullBackupQuery())
	if err != nil {
		return fmt.Errorf("listing full-backup databases: %w", err)
	}
	for _, db := range fullDBs {
		if e.cfg.EnablePlainBackups {
			e.log.Info("plain backup", "database", db)
			out := filepath.Join(dir, db+".sql.gz")
			args := append(e.baseArgs(), "-Fp")
			args = append(args, e.createFlag()...)
			args = append(args, db)
			if err := e.dumpToGzip(ctx, out, "pg_dump", args...); err != nil {
				e.log.Error("failed to produce plain backup", "database", db, "err", err)
				failures++
			}
		}
		if e.cfg.EnableCustomBackups {
			e.log.Info("custom backup", "database", db)
			out := filepath.Join(dir, db+".custom")
			// -Fc writes a compressed binary archive directly with -f.
			args := append(e.baseArgs(), "-Fc")
			args = append(args, e.createFlag()...)
			args = append(args, db, "-f", out+inProgressSuffix)
			if err := e.runDump(ctx, out, "pg_dump", args...); err != nil {
				e.log.Error("failed to produce custom backup", "database", db, "err", err)
				failures++
			}
		}
	}

	if failures > 0 {
		return fmt.Errorf("%d dump(s) failed", failures)
	}
	e.log.Info("all database backups complete", "dir", dir)
	return nil
}

const inProgressSuffix = ".in_progress"

// dumpToGzip runs a pg client and streams its stdout through gzip into
// out+".in_progress", renaming to the final name only on success (atomic).
func (e engine) dumpToGzip(ctx context.Context, out, bin string, args ...string) error {
	if e.dryRun {
		e.log.Info("dry-run: would dump", "cmd", bin, "out", out)
		return nil
	}

	tmp := out + inProgressSuffix
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("open %q: %w", tmp, err)
	}
	defer func() { _ = os.Remove(tmp) }() // no-op after successful rename

	gz := gzip.NewWriter(f)

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = gz
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	// Always close the gzip + file before deciding success.
	gzErr := gz.Close()
	fErr := f.Close()

	if runErr != nil {
		return fmt.Errorf("%s: %w: %s", bin, runErr, strings.TrimSpace(stderr.String()))
	}
	if gzErr != nil {
		return fmt.Errorf("gzip close: %w", gzErr)
	}
	if fErr != nil {
		return fmt.Errorf("file close: %w", fErr)
	}
	if err := os.Rename(tmp, out); err != nil {
		return fmt.Errorf("rename %q -> %q: %w", tmp, out, err)
	}
	return nil
}

// runDump runs a pg client that writes its own output file (e.g. pg_dump -Fc -f),
// then atomically renames the .in_progress file to its final name.
func (e engine) runDump(ctx context.Context, out, bin string, args ...string) error {
	if e.dryRun {
		e.log.Info("dry-run: would dump", "cmd", bin, "out", out)
		return nil
	}

	tmp := out + inProgressSuffix
	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%s: %w: %s", bin, err, strings.TrimSpace(stderr.String()))
	}
	if err := os.Rename(tmp, out); err != nil {
		return fmt.Errorf("rename %q -> %q: %w", tmp, out, err)
	}
	return nil
}

// listDatabases runs a query via psql -At and returns the non-empty rows.
func (e engine) listDatabases(ctx context.Context, query string) ([]string, error) {
	args := append(e.baseArgs(), "-At", "-c", query, e.cfg.Database)
	cmd := exec.CommandContext(ctx, "psql", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("psql: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	var dbs []string
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			dbs = append(dbs, s)
		}
	}
	return dbs, nil
}

func (e engine) schemaOnlyQuery() string {
	clause := ""
	for _, m := range e.cfg.SchemaOnlyList {
		clause += fmt.Sprintf(" or datname ~ '%s'", sqlEscape(m))
	}
	return fmt.Sprintf(
		"select datname from pg_database where false%s order by datname;", clause)
}

func (e engine) fullBackupQuery() string {
	clause := ""
	for _, m := range e.cfg.SchemaOnlyList {
		clause += fmt.Sprintf(" and datname !~ '%s'", sqlEscape(m))
	}
	return fmt.Sprintf(
		"select datname from pg_database where not datistemplate and datallowconn%s order by datname;",
		clause)
}

// sqlEscape doubles single quotes so a pattern can't break out of the literal.
func sqlEscape(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

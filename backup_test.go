package main

import (
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// TestDumpToGzipAtomic verifies the streaming-gzip + .in_progress->final rename,
// using `sh -c printf` as a stand-in for pg_dump so the test needs no database.
func TestDumpToGzipAtomic(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "db.sql.gz")
	e := engine{log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	const payload = "CREATE TABLE t (id int);\n"
	err := e.dumpToGzip(context.Background(), out, "sh", "-c", "printf %s "+shellQuote(payload))
	if err != nil {
		t.Fatalf("dumpToGzip: %v", err)
	}

	// The in-progress file must be gone and the final file present.
	if _, err := os.Stat(out + inProgressSuffix); !os.IsNotExist(err) {
		t.Errorf(".in_progress file should have been renamed away")
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("output is not valid gzip: %v", err)
	}
	got, _ := io.ReadAll(gz)
	if string(got) != payload {
		t.Errorf("decompressed = %q, want %q", got, payload)
	}
}

// TestDumpToGzipFailureLeavesNoFinal ensures a failing dump does not leave a
// final-named file behind (only the cleaned-up .in_progress, which is removed).
func TestDumpToGzipFailureLeavesNoFinal(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "db.sql.gz")
	e := engine{log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	if err := e.dumpToGzip(context.Background(), out, "sh", "-c", "exit 3"); err == nil {
		t.Fatal("expected error from failing command")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("no final file should exist after a failed dump")
	}
	if _, err := os.Stat(out + inProgressSuffix); !os.IsNotExist(err) {
		t.Errorf(".in_progress should have been cleaned up after failure")
	}
}

func shellQuote(s string) string {
	return "'" + s + "'"
}

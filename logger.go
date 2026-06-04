package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const defaultLogDir = "/var/log/pgbackup"

// setupLogger builds the run logger and a closer for any opened file.
//
// Behaviour:
//   - logDir empty or "-": log everything to stderr only.
//   - otherwise: log everything (INFO+) to <logDir>/pgbackup-<date>.log, and
//     additionally mirror to stderr — full output when stderr is a terminal,
//     or WARN+ only when it is not (so cron mails you on problems, not on every
//     successful run). If the log file can't be opened, fall back to stderr.
func setupLogger(logDir string, jsonLog bool, now time.Time) (*slog.Logger, func() error) {
	noop := func() error { return nil }

	if logDir == "" || logDir == "-" {
		return newLogger(os.Stderr, slog.LevelInfo, jsonLog), noop
	}

	stderrLevel := slog.LevelWarn
	if isTerminal(os.Stderr) {
		stderrLevel = slog.LevelInfo
	}

	if err := os.MkdirAll(logDir, 0o750); err != nil {
		l := newLogger(os.Stderr, slog.LevelInfo, jsonLog)
		l.Warn("could not create log dir; logging to stderr only", "log_dir", logDir, "err", err)
		return l, noop
	}

	path := filepath.Join(logDir, fmt.Sprintf("pgbackup-%s.log", now.Format(dateLayout)))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		l := newLogger(os.Stderr, slog.LevelInfo, jsonLog)
		l.Warn("could not open log file; logging to stderr only", "path", path, "err", err)
		return l, noop
	}

	h := multiHandler{handlers: []slog.Handler{
		newHandler(f, slog.LevelInfo, jsonLog),
		newHandler(os.Stderr, stderrLevel, jsonLog),
	}}
	return slog.New(h), f.Close
}

func newLogger(w *os.File, level slog.Level, jsonLog bool) *slog.Logger {
	return slog.New(newHandler(w, level, jsonLog))
}

func newHandler(w *os.File, level slog.Level, jsonLog bool) slog.Handler {
	opts := &slog.HandlerOptions{Level: level}
	if jsonLog {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// multiHandler fans a record out to several handlers, each keeping its own
// level threshold, so the same logger can write INFO+ to a file and WARN+ to
// stderr at the same time.
type multiHandler struct {
	handlers []slog.Handler
}

func (m multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m.handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil {
			return err
		}
	}
	return nil
}

func (m multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return multiHandler{handlers: next}
}

func (m multiHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		next[i] = h.WithGroup(name)
	}
	return multiHandler{handlers: next}
}

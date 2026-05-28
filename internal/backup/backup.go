// Package backup runs periodic SQLite "VACUUM INTO" snapshots of the bot's
// database and rotates old snapshots out by mtime.
//
// VACUUM INTO produces a single, atomic .db file (no WAL artefacts to copy),
// so backups are safe to run while the bot is actively scraping. Using stdlib
// SQL through modernc.org/sqlite means we stay CGO-free.
package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/julianbeese/immo_bot/internal/config"
	"github.com/julianbeese/immo_bot/internal/repository/sqlite"
)

// Vacuumer is the minimal surface backup needs. The sqlite Repository
// implements it directly. A separate interface keeps tests trivial.
type Vacuumer interface {
	VacuumInto(ctx context.Context, path string) error
}

// Run drives a ticker that snapshots the database every cfg.Interval and
// prunes snapshots older than cfg.RetentionDays. Returns when ctx is done.
// Safe to call as a goroutine from main.
func Run(ctx context.Context, repo Vacuumer, cfg config.BackupConfig, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Interval <= 0 {
		logger.Warn("backup interval is zero or negative, disabling", "interval", cfg.Interval)
		return
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		logger.Error("backup: failed to create dir, disabling", "dir", cfg.Dir, "error", err)
		return
	}

	logger.Info("backup loop started",
		"dir", cfg.Dir, "interval", cfg.Interval, "retention_days", cfg.RetentionDays)

	t := time.NewTicker(cfg.Interval)
	defer t.Stop()

	// One snapshot at startup so a freshly-restarted container immediately
	// has a recovery point.
	once(ctx, repo, cfg, logger)

	for {
		select {
		case <-ctx.Done():
			logger.Info("backup loop stopping")
			return
		case <-t.C:
			once(ctx, repo, cfg, logger)
		}
	}
}

// once writes one snapshot and prunes older files. Logged-only errors so the
// loop can't be killed by a transient disk hiccup.
func once(ctx context.Context, repo Vacuumer, cfg config.BackupConfig, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	start := time.Now()
	name := fmt.Sprintf("immobot-%s.db", start.UTC().Format("20060102-150405"))
	path := filepath.Join(cfg.Dir, name)

	if err := repo.VacuumInto(ctx, path); err != nil {
		logger.Error("backup: VACUUM INTO failed", "path", path, "error", err)
		return
	}
	info, _ := os.Stat(path)
	var size int64
	if info != nil {
		size = info.Size()
	}
	logger.Info("backup written",
		"path", path, "bytes", size, "elapsed", time.Since(start).Truncate(time.Millisecond))

	if err := prune(cfg.Dir, cfg.RetentionDays, logger); err != nil {
		logger.Warn("backup prune failed", "error", err)
	}
}

// prune deletes backup files (matching immobot-*.db) older than retentionDays.
// With retentionDays<=0 only the newest snapshot is kept.
func prune(dir string, retentionDays int, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type fileInfo struct {
		path string
		mod  time.Time
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "immobot-") || !strings.HasSuffix(name, ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{path: filepath.Join(dir, name), mod: info.ModTime()})
	}
	if len(files) == 0 {
		return nil
	}
	// Newest first so retentionDays<=0 keeps index 0.
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })

	if retentionDays <= 0 {
		// Keep only the newest backup.
		for _, f := range files[1:] {
			if err := os.Remove(f.path); err != nil {
				logger.Warn("backup: remove failed", "path", f.path, "error", err)
			} else {
				logger.Info("backup pruned", "path", f.path)
			}
		}
		return nil
	}

	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	for _, f := range files {
		if f.mod.Before(cutoff) {
			if err := os.Remove(f.path); err != nil {
				logger.Warn("backup: remove failed", "path", f.path, "error", err)
			} else {
				logger.Info("backup pruned (expired)", "path", f.path, "age_days", int(time.Since(f.mod).Hours()/24))
			}
		}
	}
	return nil
}

// Compile-time check: the sqlite repository satisfies Vacuumer.
var _ Vacuumer = (*sqlite.Repository)(nil)

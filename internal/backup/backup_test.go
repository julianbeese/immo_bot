package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/julianbeese/immo_bot/internal/config"
)

// fakeVacuumer writes a tiny placeholder file at `path` to mimic the real
// VACUUM INTO output without needing a live sqlite database.
type fakeVacuumer struct {
	calls int
	err   error
}

func (f *fakeVacuumer) VacuumInto(_ context.Context, path string) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	return os.WriteFile(path, []byte("fake-db"), 0o644)
}

func TestOnceWritesAndPrunes(t *testing.T) {
	dir := t.TempDir()
	v := &fakeVacuumer{}
	cfg := config.BackupConfig{Enabled: true, Interval: time.Hour, RetentionDays: 0, Dir: dir}
	once(context.Background(), v, cfg, nil)

	if v.calls != 1 {
		t.Fatalf("VacuumInto calls = %d, want 1", v.calls)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if !strings.HasPrefix(files[0].Name(), "immobot-") || !strings.HasSuffix(files[0].Name(), ".db") {
		t.Errorf("unexpected file name: %s", files[0].Name())
	}
}

func TestPruneKeepsOnlyNewestWhenRetentionZero(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// Three pretend backups with descending mtimes.
	for i, ago := range []time.Duration{72 * time.Hour, 24 * time.Hour, 0} {
		p := filepath.Join(dir, "immobot-"+time.Now().Add(-ago).Format("20060102-150405")+"-"+itoa(i)+".db")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		ts := now.Add(-ago)
		_ = os.Chtimes(p, ts, ts)
	}
	if err := prune(dir, 0, nil); err != nil {
		t.Fatal(err)
	}
	left, _ := os.ReadDir(dir)
	if len(left) != 1 {
		t.Fatalf("retention=0 should leave 1 file, got %d", len(left))
	}
}

func TestPruneDropsExpired(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// Old (10 days) + fresh (now). retention 7 days → old removed, fresh kept.
	old := filepath.Join(dir, "immobot-old.db")
	fresh := filepath.Join(dir, "immobot-fresh.db")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tenDaysAgo := now.Add(-10 * 24 * time.Hour)
	_ = os.Chtimes(old, tenDaysAgo, tenDaysAgo)

	if err := prune(dir, 7, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("expired backup should have been removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh backup should remain")
	}
}

func TestOnceLogsErrorWithoutPanic(t *testing.T) {
	dir := t.TempDir()
	v := &fakeVacuumer{err: errors.New("disk full")}
	cfg := config.BackupConfig{Enabled: true, Interval: time.Hour, RetentionDays: 7, Dir: dir}
	// Just ensure it returns without panicking.
	once(context.Background(), v, cfg, nil)
	if v.calls != 1 {
		t.Errorf("expected one attempted call, got %d", v.calls)
	}
}

// itoa is a tiny stand-in to keep imports minimal.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

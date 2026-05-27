package whatsapp

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// TestStoreOpens verifies the pure-Go modernc driver registered as "sqlite3"
// plus the _pragma DSN works end-to-end: whatsmeow opens the store and runs
// its migrations without cgo.
func TestStoreOpens(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wa.db")
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", dbPath)

	ctx := context.Background()
	container, err := sqlstore.New(ctx, "sqlite3", dsn, waLog.Noop)
	if err != nil {
		t.Fatalf("sqlstore.New: %v", err)
	}
	if _, err := container.GetFirstDevice(ctx); err != nil {
		t.Fatalf("GetFirstDevice: %v", err)
	}
}

package logs

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestArchiveClientLog(t *testing.T) {
	dir, prefix := t.TempDir(), t.TempDir()
	clientLog := filepath.Join(prefix, "drive_c", "ProgramData", "TrucklineMP", "Client.log")
	if err := os.MkdirAll(filepath.Dir(clientLog), 0o755); err != nil {
		t.Fatal(err)
	}

	// No Client.log exists yet.
	if err := ArchiveClientLog(prefix, dir); err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	for i := range keepClientLogs + 2 {
		if err := os.WriteFile(clientLog, []byte("run"), 0o644); err != nil {
			t.Fatal(err)
		}
		mtime := start.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(clientLog, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		// A start without a new Client.log archives the same file again.
		for range 2 {
			if err := ArchiveClientLog(prefix, dir); err != nil {
				t.Fatal(err)
			}
		}
	}

	kept, _ := filepath.Glob(filepath.Join(dir, "Client-*.log"))
	if len(kept) != keepClientLogs {
		t.Fatalf("kept %d logs, want %d", len(kept), keepClientLogs)
	}
	oldest := "Client-" + start.Add(2*time.Hour).Format("20060102-150405") + ".log"
	if filepath.Base(kept[0]) != oldest {
		t.Errorf("oldest kept log = %s, want %s", filepath.Base(kept[0]), oldest)
	}
}

func TestLaunchRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "launch.log")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := io.WriteString(Launch(dir), "new start\n"); err != nil {
		t.Fatal(err)
	}

	if b, _ := os.ReadFile(path); string(b) != "new start\n" {
		t.Errorf("launch.log has %d bytes, want only the new start", len(b))
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "launch-*.log"))
	if len(backups) != 1 {
		t.Errorf("backups = %q, want one", backups)
	}
}

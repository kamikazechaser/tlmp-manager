package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHashesRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	kept := filepath.Join(dir, "v1.2", "zeemod.scania.dc16_v1.2.zip")
	if err := os.MkdirAll(filepath.Dir(kept), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, []byte("mod"), 0o644); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "gone.scs")

	h, err := LoadHashes()
	if err != nil || len(h.Entries) != 0 {
		t.Fatalf("LoadHashes() without a file = %+v, %v", h, err)
	}
	h.Entries[kept] = Hash{Sig: "3|1", MD5: "aa"}
	h.Entries[gone] = Hash{Sig: "3|1", MD5: "bb"}
	if err := SaveHashes(h); err != nil {
		t.Fatal(err)
	}

	got, err := LoadHashes()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 || got.Entries[kept] != (Hash{Sig: "3|1", MD5: "aa"}) {
		t.Errorf("LoadHashes() = %+v, want only %s", got.Entries, kept)
	}
}

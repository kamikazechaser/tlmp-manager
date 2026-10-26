package installer

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kamikazechaser/tlmp-manager/internal/config"
	"github.com/kamikazechaser/tlmp-manager/internal/shim"
	"github.com/kamikazechaser/tlmp-manager/internal/steam"
)

type (
	// fakeCDN serves a manifest and files; a manifest hash may differ from the served file.
	fakeCDN struct {
		mu       sync.Mutex
		manifest map[string]string
		files    map[string][]byte
		gets     int
	}

	fixture struct {
		inst      *Installer
		paths     steam.Paths
		gameDir   string
		prefixDir string
	}
)

var (
	// vanilla stands in for the Microsoft dll, which a test cannot use.
	vanilla = []byte("MZ test DirectStorage DStorageGetFactory DStorageSetConfiguration1")
)

func newFakeCDN() *fakeCDN {
	return &fakeCDN{manifest: map[string]string{}, files: map[string][]byte{}}
}

func (f *fakeCDN) set(name, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[name] = []byte(content)
	f.manifest[name] = md5hex(content)
}

func (f *fakeCDN) downloads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets
}

func (f *fakeCDN) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/latest.json" {
		_ = json.NewEncoder(w).Encode(f.manifest)
		return
	}
	b, ok := f.files[strings.TrimPrefix(r.URL.Path, "/files/")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	f.gets++
	_, _ = w.Write(b)
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func newFixture(t *testing.T, cdn *fakeCDN) fixture {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	srv := httptest.NewServer(cdn)
	t.Cleanup(srv.Close)

	f := fixture{gameDir: t.TempDir(), prefixDir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(f.gameDir, dstorageFile), vanilla, 0o644); err != nil {
		t.Fatal(err)
	}
	f.paths = steam.Paths{GameDir: f.gameDir, PrefixDir: f.prefixDir}

	inst, err := New(Opts{URLs: config.NewURLs(srv.URL)})
	if err != nil {
		t.Fatal(err)
	}
	f.inst = inst
	return f
}

func (f fixture) read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (f fixture) prefixFile(name string) string {
	return destPath(name, f.prefixDir)
}

func TestRunInstallsFilesAndShim(t *testing.T) {
	cdn := newFakeCDN()
	cdn.set(coreDLL, "core v1")
	cdn.set("TrucklineMP/data/a.txt", "data v1")
	f := newFixture(t, cdn)
	ctx := context.Background()

	if f.inst.Installed(f.paths) {
		t.Fatal("Installed() before Run")
	}

	var reports int
	sum, err := f.inst.Run(ctx, f.paths, func(Progress) { reports++ })
	if err != nil {
		t.Fatal(err)
	}
	if sum.Done != 2 || sum.Total != 2 || sum.Bytes != int64(len("core v1")+len("data v1")) || reports == 0 {
		t.Errorf("Run() = %+v with %d reports", sum, reports)
	}
	if got := f.read(t, f.prefixFile("TrucklineMP/data/a.txt")); got != "data v1" {
		t.Errorf("a.txt = %q", got)
	}
	if got := f.read(t, filepath.Join(f.gameDir, dstorageFile)); got != string(shim.DLL) {
		t.Error("dstorage.dll is not the shim")
	}
	if got := f.read(t, filepath.Join(f.gameDir, dstorageOGFile)); got != string(vanilla) {
		t.Error("dstorage_og.dll is not the vanilla file")
	}

	pending, total, err := f.inst.Check(ctx, f.paths)
	if err != nil || len(pending) != 0 || total != 3 || !f.inst.Installed(f.paths) {
		t.Errorf("after Run: Check() = %v, %d, %v; Installed() = %v", pending, total, err, f.inst.Installed(f.paths))
	}

	gets := cdn.downloads()
	sum, err = f.inst.Run(ctx, f.paths, nil)
	if err != nil || sum.Total != 0 || cdn.downloads() != gets {
		t.Errorf("repeated Run() = %+v, %v; downloads %d -> %d", sum, err, gets, cdn.downloads())
	}
}

func TestRunHashMismatchKeepsPreviousFile(t *testing.T) {
	const name = "TrucklineMP/data/a.txt"
	cdn := newFakeCDN()
	cdn.set(coreDLL, "core v1")
	cdn.set(name, "data v1")
	f := newFixture(t, cdn)
	ctx := context.Background()
	if _, err := f.inst.Run(ctx, f.paths, nil); err != nil {
		t.Fatal(err)
	}

	cdn.mu.Lock()
	cdn.manifest[name] = md5hex("data v2")
	cdn.files[name] = []byte("corrupted")
	cdn.mu.Unlock()

	if _, err := f.inst.Run(ctx, f.paths, nil); err == nil {
		t.Fatal("Run() accepted a file with the wrong hash")
	}
	if got := f.read(t, f.prefixFile(name)); got != "data v1" {
		t.Errorf("a.txt = %q after a failed download, want the previous file", got)
	}
	if pending, _, err := f.inst.Check(ctx, f.paths); err != nil || len(pending) != 1 || pending[0] != name {
		t.Errorf("Check() = %v, %v; want %s pending", pending, err, name)
	}

	cdn.set(name, "data v2")
	if _, err := f.inst.Run(ctx, f.paths, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.read(t, f.prefixFile(name)); got != "data v2" {
		t.Errorf("a.txt = %q, want the new file", got)
	}
}

func TestRunRejectsUnsafeManifest(t *testing.T) {
	for _, name := range []string{
		"../outside.txt",
		"/tmp/absolute.txt",
		"TrucklineMP/../../escape.txt",
		"Other/file.txt",
	} {
		t.Run(name, func(t *testing.T) {
			cdn := newFakeCDN()
			cdn.set(name, "payload")
			f := newFixture(t, cdn)

			_, err := f.inst.Run(context.Background(), f.paths, nil)
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("Run() error = %v, want ErrUnsafePath", err)
			}
			if got := f.read(t, filepath.Join(f.gameDir, dstorageFile)); got != string(vanilla) {
				t.Error("Run() changed dstorage.dll after rejecting the manifest")
			}
		})
	}
}

func TestUninstallRestoresVanilla(t *testing.T) {
	cdn := newFakeCDN()
	cdn.set(coreDLL, "core v1")
	cdn.set("TrucklineMP/data/a.txt", "data v1")
	f := newFixture(t, cdn)
	ctx := context.Background()
	if _, err := f.inst.Run(ctx, f.paths, nil); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := f.inst.Uninstall(f.paths); err != nil {
			t.Fatalf("Uninstall() = %v", err)
		}
	}

	if got := f.read(t, filepath.Join(f.gameDir, dstorageFile)); got != string(vanilla) {
		t.Error("dstorage.dll is not the vanilla file")
	}
	for _, p := range []string{
		filepath.Join(f.gameDir, dstorageOGFile),
		f.prefixFile(coreDLL),
		f.prefixFile("TrucklineMP/data/a.txt"),
	} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still exists after Uninstall()", p)
		}
	}
}

// TestRunKeepsEarlierDstorage keeps any dll that replaced the shim, such as another mod, as the backup.
func TestRunKeepsEarlierDstorage(t *testing.T) {
	cdn := newFakeCDN()
	cdn.set(coreDLL, "core v1")
	f := newFixture(t, cdn)
	ctx := context.Background()
	if _, err := f.inst.Run(ctx, f.paths, nil); err != nil {
		t.Fatal(err)
	}

	other := "MZ another mod"
	if err := os.WriteFile(filepath.Join(f.gameDir, dstorageFile), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.inst.Run(ctx, f.paths, nil); err != nil {
		t.Fatal(err)
	}

	if got := f.read(t, filepath.Join(f.gameDir, dstorageFile)); got != string(shim.DLL) {
		t.Error("dstorage.dll is not the shim")
	}
	if got := f.read(t, filepath.Join(f.gameDir, dstorageOGFile)); got != other {
		t.Errorf("dstorage_og.dll = %q, want the other mod", got)
	}
}

func TestDisable(t *testing.T) {
	tests := []struct {
		name string
		// before changes the game folder after the install.
		before func(t *testing.T, f fixture)
		want   string
	}{
		{name: "after an install", want: string(vanilla)},
		{
			name: "game had no dstorage.dll",
			before: func(t *testing.T, f fixture) {
				if err := os.Remove(filepath.Join(f.gameDir, dstorageOGFile)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "Steam replaced the shim",
			before: func(t *testing.T, f fixture) {
				if err := os.WriteFile(filepath.Join(f.gameDir, dstorageFile), []byte("MZ v2"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "MZ v2",
		},
		{
			name: "install stopped before the shim was written",
			before: func(t *testing.T, f fixture) {
				if err := os.Remove(filepath.Join(f.gameDir, dstorageFile)); err != nil {
					t.Fatal(err)
				}
			},
			want: string(vanilla),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cdn := newFakeCDN()
			cdn.set(coreDLL, "core v1")
			f := newFixture(t, cdn)
			if _, err := f.inst.Run(context.Background(), f.paths, nil); err != nil {
				t.Fatal(err)
			}
			if tt.before != nil {
				tt.before(t, f)
			}

			for range 2 {
				if err := f.inst.Disable(f.paths); err != nil {
					t.Fatalf("Disable() = %v", err)
				}
			}

			b, err := os.ReadFile(filepath.Join(f.gameDir, dstorageFile))
			if tt.want == "" && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("dstorage.dll exists with %q, want no file", b)
			}
			if tt.want != "" && string(b) != tt.want {
				t.Errorf("dstorage.dll = %q, %v; want %q", b, err, tt.want)
			}
			if _, err := os.Stat(filepath.Join(f.gameDir, dstorageOGFile)); !errors.Is(err, os.ErrNotExist) {
				t.Error("dstorage_og.dll still exists")
			}
			if got := f.read(t, f.prefixFile(coreDLL)); got != "core v1" {
				t.Errorf("Disable() changed the downloaded core to %q", got)
			}
		})
	}
}

// TestRunAdoptsUpdatedVanilla covers a game update that replaces the shim with a new dll.
func TestRunAdoptsUpdatedVanilla(t *testing.T) {
	cdn := newFakeCDN()
	cdn.set(coreDLL, "core v1")
	f := newFixture(t, cdn)
	ctx := context.Background()
	if _, err := f.inst.Run(ctx, f.paths, nil); err != nil {
		t.Fatal(err)
	}

	updated := append(bytes.Clone(vanilla), " v2"...)
	if err := os.WriteFile(filepath.Join(f.gameDir, dstorageFile), updated, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.inst.Run(ctx, f.paths, nil); err != nil {
		t.Fatal(err)
	}

	if got := f.read(t, filepath.Join(f.gameDir, dstorageFile)); got != string(shim.DLL) {
		t.Error("dstorage.dll is not the shim")
	}
	if got := f.read(t, filepath.Join(f.gameDir, dstorageOGFile)); got != string(updated) {
		t.Error("dstorage_og.dll is not the updated vanilla file")
	}
}

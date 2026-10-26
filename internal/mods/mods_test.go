package mods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kamikazechaser/tlmp-manager/internal/state"
	"github.com/kamikazechaser/tlmp-manager/internal/steam"
)

type (
	// fixture is a Steam library with a Workshop folder, a Proton prefix, and an allowlist server.
	fixture struct {
		m        *Mods
		p        steam.Paths
		workshop string
		modDir   string

		mu   sync.Mutex
		list []rawEntry
	}
)

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	lib := filepath.Join(t.TempDir(), "steamapps")
	f := &fixture{
		p: steam.Paths{
			GameDir:   filepath.Join(lib, "common", "Euro Truck Simulator 2", "bin", "win_x64"),
			PrefixDir: filepath.Join(lib, "compatdata", "227300", "pfx"),
		},
		workshop: filepath.Join(lib, "workshop", "content", "227300"),
	}
	f.modDir = ModDir(f.p)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"mods": f.list})
	}))
	t.Cleanup(srv.Close)
	f.m = New(Opts{AllowlistURL: srv.URL, Hashes: &state.Hashes{Entries: map[string]state.Hash{}}})
	return f
}

// list puts name on the allowlist with the MD5 of content.
func (f *fixture) listed(name, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.list = append(f.list, rawEntry{Name: name, MD5: md5hex([]byte(content))})
}

// listedFolder puts name on the allowlist with the MD5 of the stored zip of dir.
func (f *fixture) listedFolder(t *testing.T, name, dir string) {
	t.Helper()
	var buf bytes.Buffer
	if err := packFolder(&buf, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	f.listed(name, buf.String())
}

func (f *fixture) check(t *testing.T) *Report {
	t.Helper()
	r, err := f.m.Check(context.Background(), f.p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (f *fixture) mod(t *testing.T, arg string) Mod {
	t.Helper()
	mod, ok := f.check(t).Find(arg)
	if !ok {
		t.Fatalf("%s is not on the list", arg)
	}
	return mod
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

// partials returns the temp files that a write left in the mod folder.
func partials(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCheck(t *testing.T) {
	f := newFixture(t)
	f.listed("100.scs", "archive")
	write(t, filepath.Join(f.workshop, "100", "latest.scs"), "archive")
	write(t, filepath.Join(f.workshop, "100", "old.scs"), "older archive")

	write(t, filepath.Join(f.workshop, "200", "versions.sii"), `package_version_info : .latest { package_name: "latest" }`)
	write(t, filepath.Join(f.workshop, "200", "latest", "manifest.sii"), "pack")
	f.listedFolder(t, "200.zip", filepath.Join(f.workshop, "200", "latest"))
	write(t, filepath.Join(f.workshop, "200", "universal", "manifest.sii"), "other")

	f.listed("300.scs", "never downloaded")
	f.listed("400.scs", "listed version")
	write(t, filepath.Join(f.workshop, "400", "universal.scs"), "other version")
	f.listed("500.scs", "deep")
	write(t, filepath.Join(f.workshop, "500", "a", "b", "c", "d", "x.scs"), "deep")
	f.listed("600.scs", "deep")
	write(t, filepath.Join(f.workshop, "600", "a", "b", "c", "x.scs"), "deep")

	f.listed("own.scs", "own")
	f.listed("enabled.scs", "on")
	write(t, filepath.Join(f.modDir, "enabled.scs"), "on")
	f.listed("old.scs", "new")
	write(t, filepath.Join(f.modDir, "old.scs"), "old")
	f.listed("off.scs", "off")
	write(t, filepath.Join(f.modDir, "off.scs.disabled"), "off")
	f.listed("ats-1.zip", "ats")
	write(t, filepath.Join(f.modDir, "gone.zip"), "left the list")
	write(t, filepath.Join(f.modDir, "notes.txt"), "not a mod")

	r := f.check(t)
	want := map[string]State{
		"100.scs":     Available,
		"200.zip":     Available,
		"300.scs":     NotSubscribed,
		"400.scs":     SteamMismatch,
		"500.scs":     NotSubscribed,
		"600.scs":     Available,
		"own.scs":     Missing,
		"enabled.scs": Enabled,
		"old.scs":     Outdated,
		"off.scs":     Disabled,
	}
	if len(r.Mods) != len(want) {
		t.Errorf("Check() has %d mods, want %d without ATS", len(r.Mods), len(want))
	}
	for _, mod := range r.Mods {
		if mod.State != want[mod.Name] {
			t.Errorf("%s: state %d, want %d", mod.Name, mod.State, want[mod.Name])
		}
	}
	if len(r.Leftovers) != 1 || r.Leftovers[0] != "gone.zip" {
		t.Errorf("Leftovers = %q, want gone.zip", r.Leftovers)
	}
	if _, ok := r.Find("100"); !ok {
		t.Error("Find() does not accept a Workshop id")
	}
	if _, ok := r.Find("OWN.SCS"); !ok {
		t.Error("Find() is case-sensitive")
	}
}

func TestWorkshopMod(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	write(t, filepath.Join(f.workshop, "200", "universal", "def", "a.sii"), "pack")
	f.listedFolder(t, "200.zip", filepath.Join(f.workshop, "200", "universal"))
	dst := filepath.Join(f.modDir, "200.zip")

	if err := f.m.Enable(ctx, f.mod(t, "200")); err != nil {
		t.Fatal(err)
	}
	if got := f.mod(t, "200"); got.State != Enabled {
		t.Fatalf("after Enable(): state %d", got.State)
	}

	kept, err := f.m.Disable(f.mod(t, "200"))
	if err != nil || kept != "" {
		t.Fatalf("Disable() = %q, %v; want the copy deleted", kept, err)
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Disable() kept %s: %v", dst, err)
	}
	if got := f.mod(t, "200"); got.State != Available {
		t.Errorf("after Disable(): state %d, want Available", got.State)
	}
}

func TestOwnMod(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.listed("PNG_PE_BASE_1.scs", "png")
	src := filepath.Join(t.TempDir(), "png_pe_base_1.scs")
	write(t, src, "png")
	dst := filepath.Join(f.modDir, "PNG_PE_BASE_1.scs")

	if _, err := f.m.Add(ctx, f.check(t), src); err != nil {
		t.Fatal(err)
	}
	if read(dst) != "png" {
		t.Fatalf("Add() did not save the file under the listed name: %s", read(dst))
	}

	kept, err := f.m.Disable(f.mod(t, "PNG_PE_BASE_1.scs"))
	if err != nil || kept != dst+".disabled" || read(kept) != "png" {
		t.Fatalf("Disable() = %q, %v; want the file kept as .disabled", kept, err)
	}
	if got := f.mod(t, "PNG_PE_BASE_1.scs"); got.State != Disabled {
		t.Fatalf("after Disable(): state %d", got.State)
	}

	if err := f.m.Enable(ctx, f.mod(t, "PNG_PE_BASE_1.scs")); err != nil {
		t.Fatal(err)
	}
	if read(dst) != "png" {
		t.Fatalf("Enable() did not rename the disabled copy back: %s", read(dst))
	}

	if err := f.m.Remove(f.check(t), "png_pe_base_1.scs"); err != nil {
		t.Fatal(err)
	}
	if got := f.mod(t, "PNG_PE_BASE_1.scs"); got.State != Missing {
		t.Errorf("after Remove(): state %d, want Missing", got.State)
	}
}

func TestAddRejects(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.listed("westplay.scs", "listed")
	dst := filepath.Join(f.modDir, "westplay.scs")
	write(t, dst, "listed")

	wrong := filepath.Join(t.TempDir(), "westplay.scs")
	write(t, wrong, "other version")
	if _, err := f.m.Add(ctx, f.check(t), wrong); !errors.Is(err, ErrMismatch) {
		t.Errorf("Add() of another version = %v, want ErrMismatch", err)
	}
	if read(dst) != "listed" {
		t.Errorf("Add() replaced the previous file: %s", read(dst))
	}
	if p := partials(t, f.modDir); len(p) != 0 {
		t.Errorf("Add() left partial files: %q", p)
	}

	unknown := filepath.Join(t.TempDir(), "unknown.scs")
	write(t, unknown, "x")
	if _, err := f.m.Add(ctx, f.check(t), unknown); !errors.Is(err, ErrNotListed) {
		t.Errorf("Add() of an unlisted name = %v, want ErrNotListed", err)
	}
}

func TestEnableStopped(t *testing.T) {
	f := newFixture(t)
	f.listed("100.scs", "archive")
	write(t, filepath.Join(f.workshop, "100", "100.scs"), "archive")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := f.m.Enable(ctx, f.mod(t, "100")); !errors.Is(err, context.Canceled) {
		t.Errorf("Enable() after cancel = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(filepath.Join(f.modDir, "100.scs")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Enable() after cancel installed the mod: %v", err)
	}
	if p := partials(t, f.modDir); len(p) != 0 {
		t.Errorf("Enable() left partial files: %q", p)
	}
}

func TestSync(t *testing.T) {
	f := newFixture(t)
	f.listed("100.scs", "new")
	write(t, filepath.Join(f.workshop, "100", "100.scs"), "new")
	write(t, filepath.Join(f.modDir, "100.scs"), "old")
	f.listed("promods-eu-map-v284.scs", "new map")
	write(t, filepath.Join(f.modDir, "promods-eu-map-v284.scs"), "old map")
	f.listed("200.scs", "not enabled")
	write(t, filepath.Join(f.workshop, "200", "200.scs"), "not enabled")

	outdated, err := f.m.Sync(context.Background(), f.p)
	if err != nil {
		t.Fatal(err)
	}
	if read(filepath.Join(f.modDir, "100.scs")) != "new" {
		t.Error("Sync() did not update the Workshop mod from Steam")
	}
	if len(outdated) != 1 || outdated[0].Name != "promods-eu-map-v284.scs" {
		t.Errorf("Sync() outdated = %+v, want the own mod only", outdated)
	}
	if _, err := os.Stat(filepath.Join(f.modDir, "200.scs")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Sync() enabled a mod: %v", err)
	}
}

func TestRemoveLeftoversAndAll(t *testing.T) {
	f := newFixture(t)
	f.listed("100.scs", "a")
	write(t, filepath.Join(f.modDir, "100.scs"), "a")
	write(t, filepath.Join(f.modDir, "own.scs.disabled"), "b")
	write(t, filepath.Join(f.modDir, "gone.zip"), "c")
	write(t, filepath.Join(f.modDir, "100.scs.123.tmp"), "partial")
	write(t, filepath.Join(f.modDir, "readme.txt"), "not ours")

	if err := f.m.Remove(f.check(t), "nothing.scs"); !errors.Is(err, ErrNotListed) {
		t.Errorf("Remove() of an unknown name = %v, want ErrNotListed", err)
	}
	if err := f.m.Remove(f.check(t), "GONE.zip"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.modDir, "gone.zip")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Remove() kept the leftover: %v", err)
	}

	if err := f.m.RemoveAll(f.p); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(f.modDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "readme.txt" {
		t.Errorf("RemoveAll() left %q, want only readme.txt", names)
	}
}

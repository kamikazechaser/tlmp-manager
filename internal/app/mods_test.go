package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kamikazechaser/tlmp-manager/internal/mods"
)

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

func TestLaunchUpdatesMods(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.srv.set(func(s *fakeServer) {
		s.mods = map[string]string{"100.scs": "new", "promods-eu-map-v284.scs": "map v2"}
	})
	write(t, filepath.Join(f.workshop, "100", "100.scs"), "new")
	write(t, filepath.Join(f.modDir, "100.scs"), "old")
	write(t, filepath.Join(f.modDir, "promods-eu-map-v284.scs"), "map v1")
	var atPlay string
	f.srv.set(func(s *fakeServer) { s.onPlay = func() { atPlay = readFile(filepath.Join(f.modDir, "100.scs")) } })

	env, err := f.app.Launch(context.Background(), f.argv)
	if err != nil {
		t.Fatal(err)
	}
	if token(env) != gameToken {
		t.Error("Launch() did not start TrucklineMP")
	}
	if atPlay != "new" {
		t.Errorf("the mod at the token request = %q, want the update from Steam", atPlay)
	}
	if !slices.ContainsFunc(f.desktop.notes, func(n string) bool {
		return strings.HasPrefix(n, "Outdated optional mods") && strings.Contains(n, "promods-eu-map-v284.scs")
	}) {
		t.Errorf("notifications = %q, want the outdated own mod", f.desktop.notes)
	}
}

func TestModsEnableWaitsForSteam(t *testing.T) {
	f := newFixture(t)
	f.srv.set(func(s *fakeServer) { s.mods = map[string]string{"300.scs": "from steam"} })
	f.desktop.onOpen = func(url string) {
		if url == "steam://url/CommunityFilePage/300" {
			write(t, filepath.Join(f.workshop, "300", "300.scs"), "from steam")
		}
	}

	if err := f.app.ModsEnable(context.Background(), []string{"300"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(filepath.Join(f.modDir, "300.scs")); got != "from steam" {
		t.Errorf("mod folder has %q, want the Steam copy", got)
	}
	if err := f.app.ModsEnable(context.Background(), []string{"unknown.scs"}); !errors.Is(err, mods.ErrNotListed) {
		t.Errorf("ModsEnable() of an unlisted mod = %v, want ErrNotListed", err)
	}
}

func TestModsListAndUninstall(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.srv.set(func(s *fakeServer) {
		s.mods = map[string]string{"100.scs": "on", "200.zip": "steam", "300.zip": "not subscribed", "promods-eu-map-v284.scs": "map"}
	})
	write(t, filepath.Join(f.modDir, "100.scs"), "on")
	write(t, filepath.Join(f.workshop, "200", "200.zip"), "steam")
	write(t, filepath.Join(f.modDir, "gone.scs"), "left the list")

	local := []string{
		"msg=Enabled file=100.scs",
		`msg=Ready file=200.zip`,
		`file=gone.scs next="tlmp-manager mods remove gone.scs"`,
	}
	global := []string{
		`msg="Not subscribed" file=300.zip`,
		`msg="Not downloaded" file=promods-eu-map-v284.scs title="Map V284"`,
		`url="https://promods.net/setup.php?game=ets"`,
	}

	if err := f.app.ModsList(ctx); err != nil {
		t.Fatal(err)
	}
	for _, want := range append(local, "mods=2") {
		if !strings.Contains(f.logs.String(), want) {
			t.Errorf("list output lacks %q:\n%s", want, f.logs)
		}
	}
	for _, unwanted := range global {
		if strings.Contains(f.logs.String(), unwanted) {
			t.Errorf("list output has %q, which only list-all shows:\n%s", unwanted, f.logs)
		}
	}

	f.logs.Reset()
	if err := f.app.ModsListAll(ctx); err != nil {
		t.Fatal(err)
	}
	for _, want := range append(local, global...) {
		if !strings.Contains(f.logs.String(), want) {
			t.Errorf("list-all output lacks %q:\n%s", want, f.logs)
		}
	}

	f.logs.Reset()
	if err := f.app.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.logs.String(), `msg="Optional mods" enabled=1 outdated=0`) {
		t.Errorf("status output:\n%s", f.logs)
	}

	f.logs.Reset()
	if err := f.app.Uninstall(ctx); err != nil {
		t.Fatal(err)
	}
	if readFile(filepath.Join(f.modDir, "100.scs")) != "on" || !strings.Contains(f.logs.String(), "mods remove --all") {
		t.Errorf("Uninstall() did not keep the mods and name them:\n%s", f.logs)
	}

	if err := f.app.ModsRemoveAll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.modDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ModsRemoveAll() kept the mod folder: %v", err)
	}
}

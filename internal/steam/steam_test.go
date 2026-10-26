package steam

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDetect finds a game on a second drive through the ~/.steam/steam symlink.
func TestDetect(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	root := filepath.Join(home, ".local", "share", "Steam")
	drive2 := filepath.Join(home, "mnt", "games", "SteamLibrary")
	write(t, filepath.Join(root, vdfSubPath), `"libraryfolders"
{
	"0" { "path" "`+root+`" }
	"1" { "path" "`+drive2+`" }
}`)
	if err := os.MkdirAll(filepath.Join(drive2, gameSubPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".steam"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(home, ".steam", "steam")); err != nil {
		t.Fatal(err)
	}

	p, err := Detect()
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{
		Root:      root,
		GameDir:   filepath.Join(drive2, gameSubPath),
		PrefixDir: filepath.Join(drive2, prefixSubPath),
	}
	if p != want {
		t.Errorf("Detect() = %+v, want %+v", p, want)
	}
}

func TestDetectSandboxed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".var/app/com.valvesoftware.Steam/.local/share/Steam"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Detect(); err == nil || Sandboxed() != "Flatpak" {
		t.Errorf("Detect() err = %v, Sandboxed() = %q; want an error and Flatpak", err, Sandboxed())
	}
}

func TestFromCommand(t *testing.T) {
	const (
		lib = "/mnt/games/SteamLibrary"
		exe = lib + "/steamapps/common/Euro Truck Simulator 2/bin/win_x64/eurotrucks2.exe"
	)
	// Steam passes this command for a Proton game, as its console log shows.
	proton := []string{
		"/home/u/.local/share/Steam/ubuntu12_32/steam-launch-wrapper", "--",
		"/home/u/.local/share/Steam/ubuntu12_32/reaper", "SteamLaunch", "AppId=227300", "--",
		"/home/u/.local/share/Steam/steamapps/common/SteamLinuxRuntime_4/_v2-entry-point", "--verb=waitforexitandrun", "--",
		"/home/u/.local/share/Steam/steamapps/common/Proton - Experimental/proton", "waitforexitandrun",
		exe, "-rdevice", "dx11",
	}
	native := []string{
		"/home/u/.local/share/Steam/ubuntu12_32/reaper", "SteamLaunch", "AppId=227300", "--",
		lib + "/steamapps/common/Euro Truck Simulator 2/bin/linux_x64/eurotrucks2",
	}

	tests := []struct {
		name   string
		argv   []string
		env    map[string]string
		want   Paths
		wantOK bool
	}{
		{
			name:   "proton with STEAM_COMPAT_DATA_PATH",
			argv:   proton,
			env:    map[string]string{"STEAM_COMPAT_DATA_PATH": lib + "/steamapps/compatdata/227300"},
			want:   Paths{GameDir: filepath.Dir(exe), PrefixDir: lib + "/steamapps/compatdata/227300/pfx"},
			wantOK: true,
		},
		{
			name:   "proton, prefix derived from the game's library",
			argv:   proton,
			want:   Paths{GameDir: filepath.Dir(exe), PrefixDir: lib + "/steamapps/compatdata/227300/pfx"},
			wantOK: true,
		},
		{name: "native Linux build", argv: native},
		{name: "no command", argv: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FromCommand(tt.argv, func(k string) string { return tt.env[k] })
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("FromCommand() = %+v, %v; want %+v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestCompatToolAndLaunchOptions(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "config", "config.vdf"), `"InstallConfigStore"
{
	"Software" { "Valve" { "Steam" {
		"CompatToolMapping"
		{
			"0" { "name" "proton_9" }
			"227300" { "name" "GE-Proton9-20" "config" "" "priority" "250" }
		}
		"SDL_GamepadBind" "line one
line two"
	} } }
}`)
	write(t, filepath.Join(root, "userdata", "111", "config", "localconfig.vdf"), `"UserLocalConfigStore"
{
	"Software" { "Valve" { "Steam" { "Apps" {
		"227300" { "LaunchOptions" "/opt/tlmp-manager launch %command%" }
	} } } }
}`)
	write(t, filepath.Join(root, "userdata", "222", "config", "localconfig.vdf"), `"UserLocalConfigStore"
{
	"Software" { "Valve" { "Steam" { "apps" { "10" { "LaunchOptions" "-x" } } } } }
}`)

	tool, err := CompatTool(root)
	if err != nil || tool != "GE-Proton9-20" || !IsProton(tool) {
		t.Errorf("CompatTool() = %q, %v; want GE-Proton9-20", tool, err)
	}
	if IsProton("steamlinuxruntime_sniper") || IsProton("") {
		t.Error("IsProton() accepted a non-Proton tool")
	}

	opts, err := LaunchOptions(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 2 || opts["111"] != "/opt/tlmp-manager launch %command%" || opts["222"] != "" {
		t.Errorf("LaunchOptions() = %q", opts)
	}
}

func TestPrefixReady(t *testing.T) {
	compat := t.TempDir()
	pfx := filepath.Join(compat, "pfx")
	if PrefixReady(pfx) {
		t.Fatal("empty compatdata reported ready")
	}
	write(t, filepath.Join(pfx, "system.reg"), "")
	if PrefixReady(pfx) {
		t.Fatal("prefix without version file reported ready")
	}
	write(t, filepath.Join(compat, "version"), "11.0-100")
	if !PrefixReady(pfx) {
		t.Fatal("finished prefix not reported ready")
	}
}

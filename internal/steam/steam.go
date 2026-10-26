package steam

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/andygrunwald/vdf"
)

const (
	appID         = "227300"
	gameSubPath   = "steamapps/common/Euro Truck Simulator 2/bin/win_x64"
	gameExe       = "/bin/win_x64/eurotrucks2.exe"
	prefixSubPath = "steamapps/compatdata/" + appID + "/pfx"
	vdfSubPath    = "steamapps/libraryfolders.vdf"
)

type (
	Paths struct {
		// Root is the Steam install that holds config/ and userdata/.
		Root      string
		GameDir   string
		PrefixDir string
	}
)

func Detect() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}

	roots := dedup([]string{
		filepath.Join(home, ".steam", "steam"),
		filepath.Join(home, ".local", "share", "Steam"),
	})

	for _, root := range roots {
		libs := dedup(append([]string{root}, parseLibraryFolders(filepath.Join(root, vdfSubPath))...))
		for _, base := range libs {
			gameDir := filepath.Join(base, gameSubPath)
			if _, err := os.Stat(gameDir); err == nil {
				return Paths{
					Root:      root,
					GameDir:   gameDir,
					PrefixDir: filepath.Join(base, prefixSubPath),
				}, nil
			}
		}
	}

	if s := Sandboxed(); s != "" {
		return Paths{}, fmt.Errorf("only %s Steam was found, which tlmp-manager does not support yet; use the regular Steam package", s)
	}
	return Paths{}, fmt.Errorf(
		"ETS2 (app %s) not found in any Steam library; use 'set game-dir' and 'set prefix-dir' to configure manually",
		appID,
	)
}

// Sandboxed names an unsupported Flatpak or Snap Steam install, or returns "".
func Sandboxed() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, s := range []struct{ name, path string }{
		{"Flatpak", ".var/app/com.valvesoftware.Steam/.local/share/Steam"},
		{"Snap", "snap/steam/common/.local/share/Steam"},
	} {
		if _, err := os.Stat(filepath.Join(home, s.path)); err == nil {
			return s.name
		}
	}
	return ""
}

// FromCommand returns false for the native Linux build, which has no eurotrucks2.exe.
func FromCommand(argv []string, getenv func(string) string) (p Paths, ok bool) {
	for _, a := range argv {
		if !strings.HasSuffix(a, gameExe) {
			continue
		}
		p.GameDir = filepath.Dir(a)
		if d := getenv("STEAM_COMPAT_DATA_PATH"); d != "" {
			p.PrefixDir = filepath.Join(d, "pfx")
		} else {
			lib := filepath.Join(p.GameDir, "..", "..", "..", "..", "..")
			p.PrefixDir = filepath.Join(lib, prefixSubPath)
		}
		return p, true
	}
	return Paths{}, false
}

func PrefixReady(prefixDir string) bool {
	_, regErr := os.Stat(filepath.Join(prefixDir, "system.reg"))
	_, verErr := os.Stat(filepath.Join(filepath.Dir(prefixDir), "version"))
	return regErr == nil && verErr == nil
}

// CompatTool returns "" when Steam starts the native Linux build.
func CompatTool(root string) (string, error) {
	m, err := parseVDF(filepath.Join(root, "config", "config.vdf"))
	if err != nil {
		return "", err
	}
	name, _ := lookup(m, "InstallConfigStore", "Software", "Valve", "Steam", "CompatToolMapping", appID, "name").(string)
	return name, nil
}

// IsProton is false for Steam Linux Runtime tools, which run the native build.
func IsProton(tool string) bool {
	return strings.Contains(strings.ToLower(tool), "proton")
}

func LaunchOptions(root string) (map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(root, "userdata", "*", "config", "localconfig.vdf"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("no Steam accounts found")
	}

	opts := make(map[string]string, len(files))
	for _, f := range files {
		m, err := parseVDF(f)
		if err != nil {
			return nil, err
		}
		id := filepath.Base(filepath.Dir(filepath.Dir(f)))
		opts[id], _ = lookup(m, "UserLocalConfigStore", "Software", "Valve", "Steam", "apps", appID, "LaunchOptions").(string)
	}
	return opts, nil
}

func parseVDF(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	m, err := vdf.NewParser(f).Parse()
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return m, nil
}

// lookup ignores key case, because Steam writes both "apps" and "Apps".
func lookup(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = nil
		for kk, v := range mm {
			if strings.EqualFold(kk, k) {
				cur = v
				break
			}
		}
	}
	return cur
}

func parseLibraryFolders(vdfPath string) []string {
	m, err := parseVDF(vdfPath)
	if err != nil {
		return nil
	}

	lf, ok := lookup(m, "libraryfolders").(map[string]any)
	if !ok {
		return nil
	}

	var paths []string
	for _, v := range lf {
		entry, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if p, ok := lookup(entry, "path").(string); ok && p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// dedup resolves symlinks, because ~/.steam/steam usually points to ~/.local/share/Steam.
func dedup(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		resolved, err := filepath.EvalSymlinks(s)
		if err != nil || seen[resolved] {
			continue
		}
		seen[resolved] = true
		out = append(out, resolved)
	}
	return out
}

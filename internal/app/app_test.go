package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kamikazechaser/tlmp-manager/internal/auth"
	"github.com/kamikazechaser/tlmp-manager/internal/config"
	"github.com/kamikazechaser/tlmp-manager/internal/installer"
	"github.com/zalando/go-keyring"
)

type (
	// fakeServer is the TrucklineMP API and download server in one.
	fakeServer struct {
		mu      sync.Mutex
		files   map[string]string
		valid   string
		cdnDown bool
		apiDown bool
	}

	fakeDesktop struct {
		notes  []string
		opened []string
		closed bool
	}

	fixture struct {
		app     *App
		srv     *fakeServer
		desktop *fakeDesktop
		logs    *bytes.Buffer
		root    string
		gameDir string
		compat  string
		argv    []string
	}
)

const (
	testExe   = "/opt/tlmp-manager"
	gameToken = "launch-token"
)

var (
	// vanilla stands in for the Microsoft dll, which a test cannot use.
	vanilla = "MZ test DirectStorage DStorageGetFactory DStorageSetConfiguration1"
)

func (s *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	reply := func(code int, v any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	switch {
	case r.URL.Path == "/latest.json":
		if s.cdnDown {
			reply(http.StatusServiceUnavailable, nil)
			return
		}
		m := map[string]string{}
		for name, content := range s.files {
			sum := md5.Sum([]byte(content))
			m[name] = hex.EncodeToString(sum[:])
		}
		reply(http.StatusOK, m)
	case strings.HasPrefix(r.URL.Path, "/files/"):
		_, _ = w.Write([]byte(s.files[strings.TrimPrefix(r.URL.Path, "/files/")]))
	case s.apiDown:
		reply(http.StatusBadGateway, map[string]string{"error": "bad gateway"})
	case r.URL.Path == "/launcher/session":
		reply(http.StatusOK, auth.LoginSession{SessionID: "pending", LoginURL: "https://example.invalid/login"})
	case r.URL.Path == "/launcher/session/pending":
		s.valid = "new-session"
		reply(http.StatusOK, map[string]string{"status": "complete", "sessionId": s.valid})
	case r.Header.Get("Authorization") != "Bearer "+s.valid:
		reply(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	case r.URL.Path == "/launcher/verify":
		reply(http.StatusOK, auth.User{Status: "valid", Username: "driver"})
	case r.URL.Path == "/launcher/play":
		reply(http.StatusOK, map[string]string{"sessionToken": gameToken})
	default:
		reply(http.StatusNotFound, nil)
	}
}

func (s *fakeServer) set(f func(*fakeServer)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

func (d *fakeDesktop) Notify(summary, body string)  { d.notes = append(d.notes, summary+": "+body) }
func (d *fakeDesktop) Progress(string, string, int) {}
func (d *fakeDesktop) Dismiss()                     {}
func (d *fakeDesktop) Close()                       { d.closed = true }

func (d *fakeDesktop) OpenURL(url string) error {
	d.opened = append(d.opened, url)
	return nil
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

// newFixture builds a Steam install whose game library is on a second drive.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	keyring.MockInit()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("STEAM_COMPAT_DATA_PATH", "")
	// Without PATH no clipboard tool runs.
	t.Setenv("PATH", t.TempDir())

	f := &fixture{
		srv:     &fakeServer{files: map[string]string{"TrucklineMP/trucklineets.dll": "core", "TrucklineMP/data/a.txt": "data"}},
		desktop: &fakeDesktop{},
		logs:    &bytes.Buffer{},
		root:    filepath.Join(home, ".local", "share", "Steam"),
	}
	lib := filepath.Join(home, "drive2", "SteamLibrary")
	f.gameDir = filepath.Join(lib, "steamapps", "common", "Euro Truck Simulator 2", "bin", "win_x64")
	f.compat = filepath.Join(lib, "steamapps", "compatdata", "227300")
	f.argv = []string{"/bin/true", filepath.Join(f.gameDir, "eurotrucks2.exe")}

	write(t, filepath.Join(f.gameDir, "dstorage.dll"), vanilla)
	write(t, filepath.Join(f.compat, "pfx", "system.reg"), "")
	write(t, filepath.Join(f.compat, "version"), "11.0-100")
	write(t, filepath.Join(f.root, "steamapps", "libraryfolders.vdf"),
		fmt.Sprintf(`"libraryfolders" { "0" { "path" "%s" } "1" { "path" "%s" } }`, f.root, lib))
	write(t, filepath.Join(f.root, "config", "config.vdf"),
		`"InstallConfigStore" { "Software" { "Valve" { "Steam" { "CompatToolMapping" { "227300" { "name" "proton_experimental" } } } } } }`)
	f.setLaunchOption(t, "1", "")

	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(f.logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	srv := httptest.NewServer(f.srv)
	t.Cleanup(srv.Close)
	inst, err := installer.New(installer.Opts{URLs: config.NewURLs(srv.URL)})
	if err != nil {
		t.Fatal(err)
	}
	f.app = New(Opts{
		API:        auth.NewClient(auth.ClientOpts{BaseURL: srv.URL, PollInterval: time.Millisecond}),
		Sessions:   auth.NewSessions(auth.SessionsOpts{Dir: filepath.Join(home, ".local", "state", "tlmp-manager")}),
		Installer:  inst,
		Desktop:    f.desktop,
		LogDir:     filepath.Join(home, ".local", "state", "tlmp-manager", "logs"),
		Executable: testExe,
	})
	return f
}

func (f *fixture) setLaunchOption(t *testing.T, account, opt string) {
	t.Helper()
	write(t, filepath.Join(f.root, "userdata", account, "config", "localconfig.vdf"),
		fmt.Sprintf(`"UserLocalConfigStore" { "Software" { "Valve" { "Steam" { "apps" { "227300" { "LaunchOptions" "%s" } } } } } }`, opt))
}

// login stores a session that the server accepts.
func (f *fixture) login(t *testing.T) {
	t.Helper()
	f.srv.set(func(s *fakeServer) { s.valid = "good-session" })
	if err := f.app.sessions.Save("good-session"); err != nil {
		t.Fatal(err)
	}
}

func token(env []string) string {
	i := slices.IndexFunc(env, func(kv string) bool { return strings.HasPrefix(kv, tokenEnv+"=") })
	if i < 0 {
		return ""
	}
	return strings.TrimPrefix(env[i], tokenEnv+"=")
}

func TestLaunch(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(t *testing.T, f *fixture)
		argv      func(f *fixture) []string
		wantToken bool
		wantNote  string
		wantLogin bool
	}{
		{
			name:      "logged in",
			prepare:   func(t *testing.T, f *fixture) { f.login(t) },
			wantToken: true,
		},
		{
			name:      "logged out, so the browser login runs",
			wantToken: true,
			wantLogin: true,
		},
		{
			name: "rejected session, so the browser login runs again",
			prepare: func(t *testing.T, f *fixture) {
				if err := f.app.sessions.Save("revoked-session"); err != nil {
					t.Fatal(err)
				}
			},
			wantToken: true,
			wantLogin: true,
		},
		{
			name:    "native Linux build",
			prepare: func(t *testing.T, f *fixture) { f.login(t) },
			argv: func(f *fixture) []string {
				return []string{"/bin/true", filepath.Join(filepath.Dir(f.gameDir), "linux_x64", "eurotrucks2")}
			},
			wantNote: "needs Proton",
		},
		{
			name: "Proton prefix not ready",
			prepare: func(t *testing.T, f *fixture) {
				f.login(t)
				if err := os.Remove(filepath.Join(f.compat, "version")); err != nil {
					t.Fatal(err)
				}
			},
			wantNote: "Proton sets up ETS2",
		},
		{
			name: "download server down and nothing installed",
			prepare: func(t *testing.T, f *fixture) {
				f.login(t)
				f.srv.set(func(s *fakeServer) { s.cdnDown = true })
			},
			wantNote: "Download failed",
		},
		{
			name: "download server down after an install",
			prepare: func(t *testing.T, f *fixture) {
				f.login(t)
				if _, err := f.app.Launch(context.Background(), f.argv); err != nil {
					t.Fatal(err)
				}
				f.srv.set(func(s *fakeServer) { s.cdnDown = true })
			},
			wantToken: true,
		},
		{
			name: "API down",
			prepare: func(t *testing.T, f *fixture) {
				f.login(t)
				f.srv.set(func(s *fakeServer) { s.apiDown = true })
			},
			wantNote: "Session token request failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			if tt.prepare != nil {
				tt.prepare(t, f)
			}
			argv := f.argv
			if tt.argv != nil {
				argv = tt.argv(f)
			}

			env, err := f.app.Launch(context.Background(), argv)
			if err != nil {
				t.Fatal(err)
			}

			if got := token(env); (got == gameToken) != tt.wantToken {
				t.Errorf("token = %q, want token: %v", got, tt.wantToken)
			}
			off := slices.ContainsFunc(f.desktop.notes, func(n string) bool {
				return strings.HasPrefix(n, "TrucklineMP is off") && strings.Contains(n, tt.wantNote)
			})
			if off != (tt.wantNote != "") {
				t.Errorf("notifications = %q, want one with %q", f.desktop.notes, tt.wantNote)
			}
			if (len(f.desktop.opened) > 0) != tt.wantLogin {
				t.Errorf("opened login pages = %q, want login: %v", f.desktop.opened, tt.wantLogin)
			}
		})
	}
}

func TestLaunchStopped(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := f.app.Launch(ctx, f.argv); err == nil {
		t.Fatal("Launch() started the game after Stop")
	}
}

func TestGameEnvReplacesOldToken(t *testing.T) {
	env := gameEnv([]string{"A=1", tokenEnv + "=old"}, "")
	if token(env) != "" || !slices.Contains(env, "A=1") {
		t.Errorf("gameEnv() = %q, want no token and the rest kept", env)
	}
	if got := token(gameEnv(env, "new")); got != "new" {
		t.Errorf("token = %q, want new", got)
	}
}

func TestSessionKeptOnlyOnTransientErrors(t *testing.T) {
	tests := []struct {
		name     string
		apiDown  bool
		wantKept bool
	}{
		{name: "rejected"},
		{name: "server error", apiDown: true, wantKept: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.srv.set(func(s *fakeServer) { s.apiDown = tt.apiDown })
			if err := f.app.sessions.Save("some-session"); err != nil {
				t.Fatal(err)
			}

			if _, err := f.app.currentUser(context.Background()); err == nil {
				t.Fatal("currentUser() accepted the session")
			}
			_, err := f.app.sessions.Load()
			if kept := err == nil; kept != tt.wantKept {
				t.Errorf("session kept = %v, want %v", kept, tt.wantKept)
			}
		})
	}
}

func TestSetupStatusUninstall(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.app.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`msg="Logged in" user=driver`, `msg="TrucklineMP updated" files=2`, `value="` + testExe + ` %command%"`, "Setup incomplete"} {
		if !strings.Contains(f.logs.String(), want) {
			t.Errorf("first setup output lacks %q:\n%s", want, f.logs)
		}
	}

	f.setLaunchOption(t, "1", testExe+" %command%")
	f.logs.Reset()
	if err := f.app.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.logs.String(), "Setup complete") || len(f.desktop.opened) != 1 {
		t.Errorf("second setup: output %q, %d logins; want All set and no new login", f.logs, len(f.desktop.opened))
	}

	f.logs.Reset()
	if err := f.app.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.logs.String(), `msg="TrucklineMP is up to date" files=3`) {
		t.Errorf("status output:\n%s", f.logs)
	}

	write(t, filepath.Join(f.app.logDir, "launch.log"), "log")
	f.logs.Reset()
	if err := f.app.Uninstall(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.app.logDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("log directory still exists: %v", err)
	}
	if !strings.Contains(f.logs.String(), "Remove the ETS2 launch options") {
		t.Errorf("uninstall output lacks the launch option reminder:\n%s", f.logs)
	}
	if b, _ := os.ReadFile(filepath.Join(f.gameDir, "dstorage.dll")); string(b) != vanilla {
		t.Error("dstorage.dll is not vanilla after Uninstall()")
	}
	if _, err := os.Stat(config.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("config still exists: %v", err)
	}
	if _, err := f.app.sessions.Load(); !errors.Is(err, auth.ErrNotLoggedIn) {
		t.Errorf("session still stored: %v", err)
	}
}

func TestStatusLaunchOptions(t *testing.T) {
	const ok = `msg="Steam launch options OK"`
	tests := []struct {
		name string
		// opts maps a Steam account to its ETS2 launch options.
		opts   map[string]string
		want   string
		wantOK bool
	}{
		{name: "full path", opts: map[string]string{"1": testExe + " %command%"}, want: ok + " account=1", wantOK: true},
		{name: "earlier launch form", opts: map[string]string{"1": testExe + " launch %command%"}, want: ok + " account=1", wantOK: true},
		{name: "with a variable", opts: map[string]string{"1": "PROTON_LOG=1 " + testExe + " %command%"}, want: ok + " account=1", wantOK: true},
		{name: "second account", opts: map[string]string{"1": "", "2": testExe + " %command%"}, want: ok + " account=2", wantOK: true},
		{name: "name without path", opts: map[string]string{"1": "tlmp-manager %command%"}, want: `msg="Wrong Steam launch options" account=1 value="tlmp-manager %command%"`},
		{name: "no game command", opts: map[string]string{"1": testExe}, want: `msg="Wrong Steam launch options" account=1 value=/opt/tlmp-manager`},
		{name: "not set", opts: map[string]string{"1": ""}, want: `value="` + testExe + ` %command%"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			for account, opt := range tt.opts {
				f.setLaunchOption(t, account, opt)
			}

			if err := f.app.Status(context.Background()); err != nil {
				t.Fatal(err)
			}

			out := f.logs.String()
			if !strings.Contains(out, tt.want) {
				t.Errorf("output lacks %q:\n%s", tt.want, out)
			}
			if asked := strings.Contains(out, "Set the ETS2 launch options"); asked == tt.wantOK {
				t.Errorf("output asks to set the launch options: %v, want %v:\n%s", asked, !tt.wantOK, out)
			}
		})
	}
}

func TestDisable(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.setLaunchOption(t, "1", testExe+" %command%")
	ctx := context.Background()
	if _, err := f.app.Launch(ctx, f.argv); err != nil {
		t.Fatal(err)
	}

	f.logs.Reset()
	if err := f.app.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	dstorage := filepath.Join(f.gameDir, "dstorage.dll")
	if b, _ := os.ReadFile(dstorage); string(b) != vanilla {
		t.Error("dstorage.dll is not vanilla after Disable()")
	}
	if !strings.Contains(f.logs.String(), "Remove the ETS2 launch options") {
		t.Errorf("disable output lacks the launch option reminder:\n%s", f.logs)
	}

	f.logs.Reset()
	env, err := f.app.Launch(ctx, f.argv)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dstorage); string(b) == vanilla || token(env) != gameToken {
		t.Error("Launch() after Disable() did not turn TrucklineMP on again")
	}
	if !strings.Contains(f.logs.String(), "TrucklineMP is up to date") {
		t.Errorf("Launch() after Disable() downloaded the files again:\n%s", f.logs)
	}
}

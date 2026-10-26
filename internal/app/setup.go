package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kamikazechaser/tlmp-manager/internal/auth"
	"github.com/kamikazechaser/tlmp-manager/internal/installer"
	"github.com/kamikazechaser/tlmp-manager/internal/steam"
)

// Setup must stay safe to repeat, because it is the default command.
func (a *App) Setup(ctx context.Context) error {
	p, err := a.findGame()
	if err != nil {
		return err
	}
	slog.Info("ETS2 found", "dir", p.GameDir)
	done := a.checkProton(p.Root)

	u, err := a.currentUser(ctx)
	if errors.Is(err, auth.ErrNotLoggedIn) || errors.Is(err, errSessionRejected) {
		u, err = a.termLogin(ctx)
	}
	if err != nil {
		slog.Warn("Login failed", "err", err)
		done = false
	} else {
		slog.Info("Logged in", "user", u.Username)
	}

	if !steam.PrefixReady(p.PrefixDir) {
		slog.Info("Proton sets up ETS2 on the first start. TrucklineMP installs on the start after that")
	} else if err := a.update(ctx, p); err != nil {
		slog.Warn("Install failed", "err", err)
		done = false
	}

	done = a.checkLaunchOptions(p.Root, true) && done
	if done {
		slog.Info("Setup complete. Start ETS2 from Steam")
	} else {
		slog.Warn("Setup incomplete. Do the steps above, then run tlmp-manager again")
	}
	return nil
}

func (a *App) Status(ctx context.Context) error {
	p, err := a.findGame()
	if err != nil {
		return err
	}
	slog.Info("ETS2 found", "dir", p.GameDir)
	a.checkProton(p.Root)
	if steam.PrefixReady(p.PrefixDir) {
		slog.Info("Proton prefix ready", "dir", p.PrefixDir)
	} else {
		slog.Warn("Proton prefix not ready. Start ETS2 once from Steam")
	}
	a.checkLaunchOptions(p.Root, false)

	switch u, err := a.currentUser(ctx); {
	case err == nil:
		slog.Info("Logged in", "user", u.Username)
	case errors.Is(err, auth.ErrNotLoggedIn), errors.Is(err, errSessionRejected):
		slog.Warn("Not logged in. Run tlmp-manager login, or start ETS2 from Steam")
	default:
		slog.Warn("Login check failed", "err", err)
	}

	switch pending, total, err := a.inst.Check(ctx, p); {
	case err != nil:
		slog.Warn("Update check failed", "err", err)
	case len(pending) == 0:
		slog.Info("TrucklineMP is up to date", "files", total)
	default:
		slog.Info("TrucklineMP updates on the next start", "files", len(pending))
	}

	slog.Info("Logs", "dir", a.logDir)
	return nil
}

func (a *App) update(ctx context.Context, p steam.Paths) error {
	started := false
	sum, err := a.inst.Run(ctx, p, func(pr installer.Progress) {
		if !started {
			started = true
			slog.Info("Downloading TrucklineMP", "files", pr.Total)
		}
	})
	if err != nil {
		return err
	}
	logUpdate(sum)
	return nil
}

func logUpdate(sum installer.Progress) {
	if sum.Total == 0 {
		slog.Info("TrucklineMP is up to date")
		return
	}
	slog.Info("TrucklineMP updated", "files", sum.Done, "size", humanBytes(sum.Bytes))
}

func (a *App) checkProton(root string) bool {
	tool, err := steam.CompatTool(root)
	switch {
	case root == "" || err != nil:
		slog.Warn("Could not read the Steam settings. Make sure that ETS2 uses Proton", "err", err)
		return true
	case steam.IsProton(tool):
		slog.Info("ETS2 uses Proton", "tool", tool)
		return true
	default:
		slog.Warn("ETS2 uses the native Linux build. Set a Proton version in Steam: ETS2 > Properties > Compatibility")
		return false
	}
}

func (a *App) checkLaunchOptions(root string, explain bool) bool {
	if a.launchOptionsSet(root) > 0 {
		slog.Info("Steam launch options set")
		return true
	}
	if !explain {
		slog.Warn("Steam launch options not set. Run tlmp-manager setup")
		return false
	}

	if tmp := filepath.Clean(os.TempDir()); strings.HasPrefix(a.exe, tmp+"/") || strings.Contains(a.exe, "/Downloads/") {
		slog.Warn("Move tlmp-manager to a permanent directory, for example ~/.local/bin, and run it again", "path", a.exe)
	}
	slog.Warn("Set the ETS2 launch options in Steam: ETS2 > Properties > General > Launch Options", "value", a.launchOptions())
	if copyToClipboard(a.launchOptions()) {
		slog.Info("Launch options copied to the clipboard")
	}
	return false
}

func (a *App) launchOptionsSet(root string) int {
	if root == "" {
		return 0
	}
	opts, err := steam.LaunchOptions(root)
	if err != nil {
		return 0
	}
	set := 0
	for _, o := range opts {
		if strings.Contains(o, a.exe) && strings.Contains(o, "%command%") {
			set++
		}
	}
	return set
}

func (a *App) launchOptions() string {
	exe := a.exe
	if strings.ContainsAny(exe, " \t'\"") {
		exe = strconv.Quote(exe)
	}
	return exe + " launch %command%"
}

func copyToClipboard(s string) bool {
	for _, c := range [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}} {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cmd := exec.CommandContext(ctx, c[0], c[1:]...)
		cmd.Stdin = strings.NewReader(s)
		err := cmd.Run()
		cancel()
		if err == nil {
			return true
		}
	}
	return false
}

func humanBytes(n int64) string {
	if n < 1_000_000 {
		return fmt.Sprintf("%d kB", n/1000)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/1e6)
}

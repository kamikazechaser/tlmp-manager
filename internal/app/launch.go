package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"

	"github.com/kamikazechaser/tlmp-manager/internal/auth"
	"github.com/kamikazechaser/tlmp-manager/internal/installer"
	"github.com/kamikazechaser/tlmp-manager/internal/logs"
	"github.com/kamikazechaser/tlmp-manager/internal/steam"
)

const (
	// tokenEnv and maxTokenLen must match TOKEN_ENV and the token buffer in shim/dstorage.c.
	tokenEnv    = "TLMP_SESSION_TOKEN"
	maxTokenLen = 255
)

// Launch returns the game environment; without a token the shim keeps the game vanilla.
func (a *App) Launch(ctx context.Context, argv []string) ([]string, error) {
	token, why, warn := a.prepare(ctx, argv)
	if ctx.Err() != nil {
		a.desktop.Dismiss()
		return nil, errors.New("stopped before the game started")
	}

	if why != "" {
		slog.Warn("Starting ETS2 without TrucklineMP", "reason", why)
		a.desktop.Notify("TrucklineMP is off", why)
	} else {
		slog.Info("Starting ETS2 with TrucklineMP")
		// The desktop keeps one notification, so the warning replaces the progress.
		if warn != "" {
			a.desktop.Notify("Outdated optional mods", warn)
		} else {
			a.desktop.Dismiss()
		}
	}
	return gameEnv(os.Environ(), token), nil
}

// prepare returns why TrucklineMP is off, or a warning when it is on.
func (a *App) prepare(ctx context.Context, argv []string) (token, why, warn string) {
	p, ok := steam.FromCommand(argv, os.Getenv)
	if !ok {
		return "", "TrucklineMP needs Proton. Set a Proton version in Steam: ETS2 > Properties > Compatibility.", ""
	}
	slog.Info("Paths from Steam", "game_dir", p.GameDir, "prefix_dir", p.PrefixDir)

	// Writing into a prefix that Proton has not created can break its setup.
	if !steam.PrefixReady(p.PrefixDir) {
		return "", "Proton sets up ETS2 on the first start. Start ETS2 again to play TrucklineMP.", ""
	}

	a.syncConfig(p)
	if err := logs.ArchiveClientLog(p.PrefixDir, a.logDir); err != nil {
		slog.Warn("Could not keep the previous Client.log", "err", err)
	}

	sessionID, err := a.sessions.Load()
	if errors.Is(err, auth.ErrNotLoggedIn) {
		sessionID, err = a.launchLogin(ctx)
	}
	if err != nil {
		slog.Error("Login failed", "err", err)
		return "", "Login failed: " + err.Error(), ""
	}

	sum, err := a.inst.Run(ctx, p, a.showProgress)
	switch {
	case err == nil:
		logUpdate(sum)
	case ctx.Err() != nil:
		return "", "", ""
	case a.inst.Installed(p):
		slog.Warn("Update failed. Starting with the installed version", "err", err)
	default:
		slog.Error("Download failed", "err", err)
		return "", "Download failed: " + err.Error(), ""
	}

	warn = a.syncMods(ctx, p)

	// The token is short-lived, so get it after the updates.
	token, err = a.api.Play(ctx, sessionID)
	if errors.Is(err, auth.ErrUnauthorized) {
		slog.Info("Session rejected. Logging in again")
		if err := a.sessions.Delete(); err != nil {
			slog.Warn("Could not delete stored session", "err", err)
		}
		if sessionID, err = a.launchLogin(ctx); err == nil {
			token, err = a.api.Play(ctx, sessionID)
		}
	}
	if err != nil {
		slog.Error("Session token request failed", "err", err)
		return "", "Session token request failed: " + err.Error(), ""
	}
	if len(token) > maxTokenLen {
		return "", fmt.Sprintf("The session token has %d characters. The limit is %d.", len(token), maxTokenLen), ""
	}
	return token, "", warn
}

func (a *App) launchLogin(ctx context.Context) (string, error) {
	sessionID, u, err := auth.Login(ctx, a.api, a.sessions, func(url string) error {
		if err := a.desktop.OpenURL(url); err != nil {
			return fmt.Errorf("the login page could not be opened (%w); run 'tlmp-manager login'", err)
		}
		a.desktop.Notify("Log in to TrucklineMP", "Log in in the browser. ETS2 starts after the login.")
		slog.Info("Waiting for the browser login")
		return nil
	})
	if err != nil {
		return "", err
	}
	slog.Info("Logged in", "user", u.Username)
	return sessionID, nil
}

func (a *App) showProgress(p installer.Progress) {
	done := float64(p.Done)
	if p.FileFrac > 0 {
		done += p.FileFrac
	}
	a.desktop.Progress("Updating TrucklineMP",
		fmt.Sprintf("File %d of %d, %s", min(p.Done+1, p.Total), p.Total, humanBytes(p.Bytes)),
		int(100*done/float64(max(p.Total, 1))))
}

func gameEnv(environ []string, token string) []string {
	env := slices.DeleteFunc(slices.Clone(environ), func(kv string) bool {
		return strings.HasPrefix(kv, tokenEnv+"=")
	})
	if token != "" {
		env = append(env, tokenEnv+"="+token)
	}
	return env
}

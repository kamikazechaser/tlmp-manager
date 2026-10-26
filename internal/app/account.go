package app

import (
	"context"
	"errors"
	"log/slog"

	"github.com/kamikazechaser/tlmp-manager/internal/auth"
)

var (
	errSessionRejected = errors.New("your TrucklineMP login expired or was revoked")
)

func (a *App) Login(ctx context.Context) error {
	u, err := a.termLogin(ctx)
	if err != nil {
		return err
	}
	slog.Info("Logged in", "user", u.Username)
	return nil
}

func (a *App) Logout(context.Context) error {
	if err := a.sessions.Delete(); err != nil {
		return err
	}
	slog.Info("Logged out")
	return nil
}

func (a *App) termLogin(ctx context.Context) (*auth.User, error) {
	_, u, err := auth.Login(ctx, a.api, a.sessions, func(url string) error {
		slog.Info("Log in to TrucklineMP in the browser", "url", url)
		if err := a.desktop.OpenURL(url); err != nil {
			slog.Debug("Could not open the browser", "err", err)
		}
		return nil
	})
	return u, err
}

func (a *App) currentUser(ctx context.Context) (*auth.User, error) {
	sessionID, err := a.sessions.Load()
	if err != nil {
		return nil, err
	}
	u, err := a.api.Verify(ctx, sessionID)
	if err != nil {
		return nil, a.checkRejected(err)
	}
	return u, nil
}

// checkRejected deletes the session on HTTP 401 or 403 and keeps it on transient errors.
func (a *App) checkRejected(err error) error {
	if !errors.Is(err, auth.ErrUnauthorized) {
		return err
	}
	if derr := a.sessions.Delete(); derr != nil {
		slog.Warn("Could not delete stored session", "err", derr)
	}
	return errSessionRejected
}

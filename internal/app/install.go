package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/kamikazechaser/tlmp-manager/internal/config"
	"github.com/kamikazechaser/tlmp-manager/internal/steam"
)

func (a *App) Update(ctx context.Context) error {
	p, err := a.findGame()
	if err != nil {
		return err
	}
	if !steam.PrefixReady(p.PrefixDir) {
		return errPrefixNotReady
	}

	return a.update(ctx, p)
}

// Disable keeps the login and the downloads, so the next start through tlmp-manager turns TrucklineMP on.
func (a *App) Disable(context.Context) error {
	p, err := a.findGame()
	if err != nil {
		return err
	}
	if err := a.inst.Disable(p); err != nil {
		return fmt.Errorf("could not turn TrucklineMP off: %w", err)
	}
	slog.Info("TrucklineMP is off")
	a.remindLaunchOptions(p.Root)
	return nil
}

func (a *App) Uninstall(context.Context) error {
	p, err := a.findGame()
	if err != nil {
		return err
	}
	if err := a.inst.Uninstall(p); err != nil {
		return fmt.Errorf("these TrucklineMP files could not be removed: %w", err)
	}
	if err := os.Remove(config.Path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("deleting the config: %w", err)
	}
	if err := a.sessions.Delete(); err != nil {
		return fmt.Errorf("deleting the stored login: %w", err)
	}
	if err := os.RemoveAll(a.logDir); err != nil {
		return fmt.Errorf("deleting the logs: %w", err)
	}

	slog.Info("TrucklineMP removed")
	a.remindLaunchOptions(p.Root)
	return nil
}

func (a *App) remindLaunchOptions(root string) {
	if root == "" || a.launchOptionsSet(root) > 0 {
		slog.Warn("Remove the ETS2 launch options in Steam, or the next start turns TrucklineMP on again")
	}
}

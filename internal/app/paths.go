package app

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/kamikazechaser/tlmp-manager/internal/config"
	"github.com/kamikazechaser/tlmp-manager/internal/steam"
)

var (
	errPrefixNotReady = errors.New("the Proton prefix is not ready: start ETS2 from Steam once, then try again")
)

// findGame prefers the configured paths, because 'set' and launch write them.
func (a *App) findGame() (steam.Paths, error) {
	p, detectErr := steam.Detect()

	cfg, err := config.Load()
	if err != nil {
		return steam.Paths{}, err
	}
	if cfg.GameDir != "" {
		if _, err := os.Stat(cfg.GameDir); err == nil {
			p.GameDir = cfg.GameDir
			if cfg.PrefixDir != "" {
				p.PrefixDir = cfg.PrefixDir
			}
			return p, nil
		}
	}
	if detectErr != nil {
		return steam.Paths{}, detectErr
	}

	cfg.GameDir, cfg.PrefixDir = p.GameDir, p.PrefixDir
	if err := config.Save(cfg); err != nil {
		return steam.Paths{}, err
	}
	slog.Debug("Detected Steam paths", "game_dir", cfg.GameDir, "prefix_dir", cfg.PrefixDir)
	return p, nil
}

func (a *App) syncConfig(p steam.Paths) {
	cfg, err := config.Load()
	if err != nil {
		slog.Warn("Could not load config; replacing it", "err", err)
		cfg = &config.Config{}
	}
	if samePath(cfg.GameDir, p.GameDir) && samePath(cfg.PrefixDir, p.PrefixDir) {
		return
	}

	cfg.GameDir, cfg.PrefixDir = p.GameDir, p.PrefixDir
	if err := config.Save(cfg); err != nil {
		slog.Warn("Could not save the paths from Steam", "err", err)
	}
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

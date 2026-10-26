package main

import (
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/kamikazechaser/tlmp-manager/internal/app"
	"github.com/kamikazechaser/tlmp-manager/internal/auth"
	"github.com/kamikazechaser/tlmp-manager/internal/config"
	"github.com/kamikazechaser/tlmp-manager/internal/desktop"
	"github.com/kamikazechaser/tlmp-manager/internal/installer"
	"github.com/kamikazechaser/tlmp-manager/internal/logs"
	"github.com/kamikazechaser/tlmp-manager/internal/state"
)

// initLaunchLogger also writes to launch.log, because Steam discards the output of launch.
func initLaunchLogger() {
	log.SetOutput(io.MultiWriter(os.Stderr, logs.Launch(state.LogDir())))
}

func loadApp() (*app.App, error) {
	inst, err := installer.New(installer.Opts{URLs: config.NewURLs(cdnBase)})
	if err != nil {
		return nil, err
	}
	return app.New(app.Opts{
		API:        auth.NewClient(auth.ClientOpts{BaseURL: apiBase}),
		Sessions:   auth.NewSessions(auth.SessionsOpts{Dir: state.Dir()}),
		Installer:  inst,
		Desktop:    desktop.New(desktop.Opts{AppName: "TrucklineMP"}),
		LogDir:     state.LogDir(),
		Executable: executable(),
	}), nil
}

func executable() string {
	exe, err := os.Executable()
	if err != nil {
		return "tlmp-manager"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

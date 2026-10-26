// Package app holds the tlmp-manager use cases; cmd builds the App and calls one method.
package app

import (
	"github.com/kamikazechaser/tlmp-manager/internal/auth"
	"github.com/kamikazechaser/tlmp-manager/internal/installer"
)

type (
	Desktop interface {
		Notify(summary, body string)
		Progress(summary, body string, percent int)
		Dismiss()
		OpenURL(url string) error
		Close()
	}

	Opts struct {
		API       *auth.Client
		Sessions  *auth.Sessions
		Installer *installer.Installer
		Desktop   Desktop
		LogDir    string
		// Executable is the path that the Steam launch options must run.
		Executable string
	}

	// App is the dependency container of the tlmp-manager use cases.
	App struct {
		api      *auth.Client
		sessions *auth.Sessions
		inst     *installer.Installer
		desktop  Desktop
		logDir   string
		exe      string
	}
)

func New(o Opts) *App {
	return &App{
		api:      o.API,
		sessions: o.Sessions,
		inst:     o.Installer,
		desktop:  o.Desktop,
		logDir:   o.LogDir,
		exe:      o.Executable,
	}
}

func (a *App) Close() {
	a.desktop.Close()
}

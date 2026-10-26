package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/kamikazechaser/tlmp-manager/internal/app"
	"github.com/kamikazechaser/tlmp-manager/internal/config"
	"github.com/urfave/cli/v3"
)

var (
	apiBase  = "http://localhost"
	cdnBase  = "http://localhost"
	gameBase = "http://localhost"
	version  = "dev"
)

func init() {
	if os.Getenv("DEBUG") != "" {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}
}

func main() {
	// Accept a Steam launch option that leaves out the word "launch".
	args := os.Args
	if len(args) > 1 && args[1] != "launch" && isGameCommand(args[1:]) {
		args = slices.Insert(args, 1, "launch")
	}

	cli.UsageCommandHelp = "Show help for a command"

	cmd := &cli.Command{
		Name:    "tlmp-manager",
		Usage:   "Install and launch TrucklineMP for ETS2",
		Version: version,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Args().Present() {
				return fmt.Errorf("unknown command %q; see 'tlmp-manager --help'", cmd.Args().First())
			}
			return withApp((*app.App).Setup)(ctx, cmd)
		},
		Commands: []*cli.Command{
			{
				Name:   "setup",
				Usage:  "Find ETS2, log in, and install TrucklineMP",
				Action: withApp((*app.App).Setup),
			},
			{
				Name:            "launch",
				Usage:           "Start ETS2 with TrucklineMP",
				Hidden:          true,
				ArgsUsage:       "<game command...>",
				SkipFlagParsing: true,
				Action:          launch,
			},
			{Name: "status", Usage: "Show the game path, login, and updates", Action: withApp((*app.App).Status)},
			{Name: "disable", Usage: "Turn TrucklineMP off", Action: withApp((*app.App).Disable)},
			{Name: "logout", Usage: "Delete the saved login", Action: withApp((*app.App).Logout)},
			{Name: "uninstall", Usage: "Remove TrucklineMP, the saved login, and local settings", Action: withApp((*app.App).Uninstall)},
			{
				Name:  "mods",
				Usage: "List, enable, and remove optional TrucklineMP mods",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Present() {
						return fmt.Errorf("unknown command %q; see 'tlmp-manager mods --help'", cmd.Args().First())
					}
					return withApp((*app.App).ModsList)(ctx, cmd)
				},
				Commands: []*cli.Command{
					{Name: "list", Usage: "Show enabled mods and mods that are ready to enable", Action: withApp((*app.App).ModsList)},
					{Name: "list-all", Usage: "Show every mod on the TrucklineMP list and the next step", Action: withApp((*app.App).ModsListAll)},
					{
						Name:      "enable",
						Usage:     "Copy mods from Steam into the mod folder; subscribe in Steam if necessary",
						ArgsUsage: "<file or Workshop id...>",
						Action:    withArgs((*app.App).ModsEnable),
					},
					{
						Name:      "disable",
						Usage:     "Turn mods off; keep own mods as <name>.disabled",
						ArgsUsage: "<file or Workshop id...>",
						Action:    withArgs((*app.App).ModsDisable),
					},
					{
						Name:      "remove",
						Usage:     "Delete mods and their disabled copies",
						ArgsUsage: "<file or Workshop id...>",
						Flags:     []cli.Flag{&cli.BoolFlag{Name: "all", Usage: "delete every mod file in the mod folder"}},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							if cmd.Bool("all") {
								return withApp((*app.App).ModsRemoveAll)(ctx, cmd)
							}
							return withArgs((*app.App).ModsRemove)(ctx, cmd)
						},
					},
					{
						Name:      "add",
						Usage:     "Copy own mods, such as ProMods, that you downloaded",
						ArgsUsage: "<file...>",
						Action:    withArgs((*app.App).ModsAdd),
					},
				},
			},
			{Name: "login", Category: "advanced", Usage: "Log in with your browser", Action: withApp((*app.App).Login)},
			{Name: "update", Category: "advanced", Usage: "Install TrucklineMP updates manually", Action: withApp((*app.App).Update)},
			{
				Name:     "set",
				Category: "advanced",
				Usage:    "Set the ETS2 or Proton directory",
				Commands: []*cli.Command{
					setPath("game-dir", "the ETS2 bin/win_x64 directory", func(c *config.Config, p string) { c.GameDir = p }),
					setPath("prefix-dir", "the Proton compatdata/227300/pfx directory", func(c *config.Config, p string) { c.PrefixDir = p }),
				},
			},
		},
	}

	// Steam's Stop button sends SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	err := cmd.Run(ctx, args)
	stop()
	if err != nil {
		slog.Error("Fatal error", "err", err)
		os.Exit(1)
	}
}

func withApp(run func(*app.App, context.Context) error) cli.ActionFunc {
	return func(ctx context.Context, _ *cli.Command) error {
		a, err := loadApp()
		if err != nil {
			return err
		}
		defer a.Close()
		return run(a, ctx)
	}
}

func withArgs(run func(*app.App, context.Context, []string) error) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		if !cmd.Args().Present() {
			return errors.New("name at least one mod; see 'tlmp-manager mods list-all'")
		}
		return withApp(func(a *app.App, ctx context.Context) error {
			return run(a, ctx, cmd.Args().Slice())
		})(ctx, cmd)
	}
}

// launch replaces this process with the game, so Steam tracks the game itself.
func launch(ctx context.Context, cmd *cli.Command) error {
	argv := cmd.Args().Slice()
	if len(argv) == 0 {
		return errors.New("no game command; run tlmp-manager setup to see the ETS2 launch options")
	}
	if argv[0] == "-h" || argv[0] == "--help" {
		return cli.ShowSubcommandHelp(cmd)
	}

	initLaunchLogger()
	slog.Info("Launch", "version", version, "argv", argv)

	env := os.Environ()
	a, err := loadApp()
	if err != nil {
		slog.Error("Starting ETS2 without TrucklineMP", "err", err)
	} else {
		env, err = a.Launch(ctx, argv)
		a.Close()
		if err != nil {
			return err
		}
	}

	path, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(path, argv, env)
}

func isGameCommand(args []string) bool {
	return slices.ContainsFunc(args, func(a string) bool {
		base := filepath.Base(a)
		return base == "eurotrucks2.exe" || base == "eurotrucks2"
	})
}

func setPath(name, what string, apply func(*config.Config, string)) *cli.Command {
	return &cli.Command{
		Name:      name,
		Usage:     "Set " + what,
		ArgsUsage: "<path>",
		Action: func(_ context.Context, cmd *cli.Command) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			apply(cfg, cmd.Args().First())
			return config.Save(cfg)
		},
	}
}

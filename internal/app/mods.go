package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kamikazechaser/tlmp-manager/internal/mods"
	"github.com/kamikazechaser/tlmp-manager/internal/steam"
)

const (
	steamPoll = 3 * time.Second
	// steamWait bounds the wait for a Workshop download after the player subscribes.
	steamWait = 10 * time.Minute
)

var (
	plainArg = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// ModsList shows the mods in the mod folder and the mods that Steam has ready to enable.
func (a *App) ModsList(ctx context.Context) error {
	return a.listMods(ctx, false)
}

// ModsListAll shows every mod on the TrucklineMP list.
func (a *App) ModsListAll(ctx context.Context) error {
	return a.listMods(ctx, true)
}

func (a *App) listMods(ctx context.Context, all bool) error {
	_, r, err := a.checkMods(ctx)
	if err != nil {
		return err
	}
	list := slices.Clone(r.Mods)
	if !all {
		list = slices.DeleteFunc(list, func(mod mods.Mod) bool { return !local(mod) })
	}
	titles := a.mods.Titles(ctx, list)
	slices.SortStableFunc(list, func(x, y mods.Mod) int {
		return cmp.Or(cmp.Compare(x.State, y.State), strings.Compare(strings.ToLower(titles[x.Name]), strings.ToLower(titles[y.Name])))
	})

	for _, mod := range list {
		logMod(mod, titles[mod.Name])
	}
	for _, l := range r.Leftovers {
		slog.Info("No longer on the TrucklineMP list", "file", l, "next", "tlmp-manager mods remove "+shellArg(l))
	}
	slog.Info("Mod folder", "dir", r.Dir)
	if hidden := len(r.Mods) - len(list); hidden > 0 {
		slog.Info("More mods are on the TrucklineMP list. Run tlmp-manager mods list-all", "mods", hidden)
	}
	return nil
}

// ModsEnable opens the Steam page of each mod that the player has not subscribed to, then waits for Steam.
func (a *App) ModsEnable(ctx context.Context, args []string) error {
	_, r, err := a.checkMods(ctx)
	if err != nil {
		return err
	}
	var errs []error
	var subscribe []mods.Mod
	for _, arg := range args {
		mod, ok := r.Find(arg)
		switch {
		case !ok:
			errs = append(errs, notListed(arg))
		case mod.State == mods.Enabled:
			slog.Info("Already enabled", "file", mod.Name)
		case mod.State == mods.NotSubscribed:
			subscribe = append(subscribe, mod)
		case mod.State == mods.Disabled || mod.SteamCopy:
			errs = append(errs, a.enableMod(ctx, mod))
		default:
			errs = append(errs, noCopy(mod))
		}
	}
	errs = append(errs, a.waitForSteam(ctx, subscribe))
	return errors.Join(errs...)
}

func (a *App) ModsDisable(ctx context.Context, args []string) error {
	_, r, err := a.checkMods(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, arg := range args {
		mod, ok := r.Find(arg)
		if !ok {
			errs = append(errs, notListed(arg))
			continue
		}
		if mod.State != mods.Enabled && mod.State != mods.Outdated {
			slog.Info("Not enabled", "file", mod.Name)
			continue
		}
		switch kept, err := a.mods.Disable(mod); {
		case err != nil:
			errs = append(errs, fmt.Errorf("disabling %s: %w", mod.Name, err))
		case kept != "":
			slog.Info("Disabled. The file stays for 'mods enable'", "file", mod.Name, "kept", kept)
		default:
			slog.Info("Disabled. Steam keeps its copy", "file", mod.Name)
		}
	}
	return errors.Join(errs...)
}

func (a *App) ModsRemove(ctx context.Context, args []string) error {
	_, r, err := a.checkMods(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, arg := range args {
		if err := a.mods.Remove(r, arg); err != nil {
			errs = append(errs, err)
			continue
		}
		slog.Info("Removed", "mod", arg)
	}
	return errors.Join(errs...)
}

// ModsRemoveAll does not need the mod list, so it also works offline.
func (a *App) ModsRemoveAll(context.Context) error {
	p, err := a.findGame()
	if err != nil {
		return err
	}
	if err := a.mods.RemoveAll(p); err != nil {
		return fmt.Errorf("these mod files could not be removed: %w", err)
	}
	slog.Info("Removed all optional mods", "dir", mods.ModDir(p))
	return nil
}

// ModsAdd copies own mods, such as ProMods, that the player downloaded.
func (a *App) ModsAdd(ctx context.Context, files []string) error {
	_, r, err := a.checkMods(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, file := range files {
		mod, err := a.mods.Add(ctx, r, file)
		if errors.Is(err, mods.ErrNotListed) {
			err = fmt.Errorf("%w; the file name must be as on 'tlmp-manager mods list-all'", err)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		slog.Info("Enabled", "file", mod.Name)
	}
	return errors.Join(errs...)
}

// syncMods returns a warning for the desktop when enabled mods stay outdated.
func (a *App) syncMods(ctx context.Context, p steam.Paths) string {
	outdated, err := a.mods.Sync(ctx, p)
	if err != nil {
		slog.Warn("Optional mod check failed", "err", err)
		return ""
	}
	if len(outdated) == 0 {
		return ""
	}
	names := make([]string, len(outdated))
	for i, mod := range outdated {
		names[i] = mod.Name
	}
	slog.Warn("Outdated optional mods. The server can reject the login", "files", names)
	return "Update or disable " + strings.Join(names, ", ") + ". Run 'tlmp-manager mods list'."
}

func (a *App) statusMods(ctx context.Context, p steam.Paths) {
	r, err := a.mods.Check(ctx, p)
	if err != nil {
		slog.Warn("Optional mod check failed", "err", err)
		return
	}
	enabled, outdated := 0, 0
	for _, mod := range r.Mods {
		switch mod.State {
		case mods.Enabled:
			enabled++
		case mods.Outdated:
			outdated++
		}
	}
	slog.Info("Optional mods", "enabled", enabled, "outdated", outdated)
	if outdated > 0 {
		slog.Warn("Update or disable the outdated optional mods. Run tlmp-manager mods list")
	}
}

func (a *App) checkMods(ctx context.Context) (steam.Paths, *mods.Report, error) {
	p, err := a.findGame()
	if err != nil {
		return p, nil, err
	}
	if !steam.PrefixReady(p.PrefixDir) {
		return p, nil, errPrefixNotReady
	}
	r, err := a.mods.Check(ctx, p)
	return p, r, err
}

func (a *App) enableMod(ctx context.Context, mod mods.Mod) error {
	if err := a.mods.Enable(ctx, mod); err != nil {
		return fmt.Errorf("enabling %s: %w", mod.Name, err)
	}
	slog.Info("Enabled", "file", mod.Name)
	return nil
}

// waitForSteam checks at once, and then every steamPoll, because Steam may already have the item.
func (a *App) waitForSteam(ctx context.Context, pending []mods.Mod) error {
	if len(pending) == 0 {
		return nil
	}
	for _, mod := range pending {
		if err := a.desktop.OpenURL(mod.SteamURL()); err != nil {
			slog.Warn("Could not open Steam", "err", err)
		}
		slog.Info("Subscribe in Steam", "file", mod.Name, "page", mod.WorkshopURL())
	}

	ctx, cancel := context.WithTimeout(ctx, steamWait)
	defer cancel()
	t := time.NewTicker(steamPoll)
	defer t.Stop()
	var errs []error
	for first := true; ; first = false {
		pending = slices.DeleteFunc(pending, func(mod mods.Mod) bool {
			if mod = a.mods.Refresh(mod); !mod.SteamCopy {
				return false
			}
			errs = append(errs, a.enableMod(ctx, mod))
			return true
		})
		if len(pending) == 0 {
			return errors.Join(errs...)
		}
		if first {
			slog.Info("Waiting for Steam to download the mods. Press Ctrl+C to stop")
		}
		select {
		case <-ctx.Done():
			for _, mod := range pending {
				errs = append(errs, fmt.Errorf("%s: Steam did not download it (%w); run 'tlmp-manager mods enable %s' again", mod.Name, ctx.Err(), shellArg(mod.WorkshopID)))
			}
			return errors.Join(errs...)
		case <-t.C:
		}
	}
}

// local is true for a mod in the mod folder, or one that Steam has ready to enable.
func local(mod mods.Mod) bool {
	switch mod.State {
	case mods.Enabled, mods.Outdated, mods.Disabled, mods.Available:
		return true
	default:
		return false
	}
}

func logMod(mod mods.Mod, title string) {
	attrs := []any{"file", mod.Name, "title", title}
	enable := "tlmp-manager mods enable " + shellArg(cmp.Or(mod.WorkshopID, mod.Name))
	switch mod.State {
	case mods.Enabled:
		slog.Info("Enabled", attrs...)
	case mods.Outdated:
		slog.Warn("Outdated", append(attrs, "next", nextForOutdated(mod, enable))...)
	case mods.Disabled:
		slog.Info("Disabled", append(attrs, "next", enable)...)
	case mods.Available:
		slog.Info("Ready", append(attrs, "next", enable)...)
	case mods.SteamMismatch:
		slog.Info("Steam has another version", append(attrs, "next", "wait for Steam or for the TrucklineMP list to update")...)
	case mods.NotSubscribed:
		slog.Info("Not subscribed", append(attrs, "next", enable)...)
	default:
		if url := mod.DownloadURL(); url != "" {
			attrs = append(attrs, "url", url)
		}
		slog.Info("Not downloaded", append(attrs, "next", "download it, then run tlmp-manager mods add <file>")...)
	}
}

func nextForOutdated(mod mods.Mod, enable string) string {
	switch {
	case mod.SteamCopy:
		return enable
	case mod.WorkshopID != "":
		return "wait for Steam to update it, or run tlmp-manager mods disable " + shellArg(mod.Name)
	default:
		return "download the listed version and run tlmp-manager mods add <file>, or run tlmp-manager mods disable " + shellArg(mod.Name)
	}
}

func noCopy(mod mods.Mod) error {
	if mod.WorkshopID == "" {
		hint := "download it, then run 'tlmp-manager mods add <file>'"
		if url := mod.DownloadURL(); url != "" {
			hint = "download it from " + url + ", then run 'tlmp-manager mods add <file>'"
		}
		return fmt.Errorf("%s is not from the Steam Workshop: %s", mod.Name, hint)
	}
	return fmt.Errorf("%s: Steam does not have the version on the TrucklineMP list yet; wait for Steam to update it", mod.Name)
}

func notListed(arg string) error {
	return fmt.Errorf("%s: %w; see 'tlmp-manager mods list-all'", arg, mods.ErrNotListed)
}

// shellArg quotes names such as zzPNG_StreetLamps_[FreeAddon].scs, which a shell would expand.
func shellArg(s string) string {
	if plainArg.MatchString(s) {
		return s
	}
	return strconv.Quote(s)
}

// Package mods installs the optional TrucklineMP mods into the mod folder of the Proton prefix.
package mods

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kamikazechaser/tlmp-manager/internal/state"
	"github.com/kamikazechaser/tlmp-manager/internal/steam"
)

type (
	Opts struct {
		AllowlistURL string
		// DetailsURL defaults to the Steam Web API.
		DetailsURL string
		Hashes     *state.Hashes
	}

	Mods struct {
		allowlistURL string
		detailsURL   string
		hashes       *state.Hashes
		httpClient   *http.Client
	}

	State int

	// Mod is an allowlist entry and what the mod folder and Steam have for it.
	Mod struct {
		Entry

		State State
		// SteamCopy is true when Steam has the bytes that the allowlist names.
		SteamCopy bool

		dst     string
		itemDir string
		source  source
	}

	// Report compares the mod folder with the allowlist.
	Report struct {
		Dir  string
		Mods []Mod
		// Leftovers are mod files whose names left the list. The core does not mount them.
		Leftovers []string
	}

	ctxWriter struct {
		ctx context.Context
		w   io.Writer
	}
)

const (
	// Enabled is the first State, because the states sort in the order of the mod list.
	Enabled State = iota + 1
	// Outdated is a file in the mod folder with another MD5. The server can reject the login.
	Outdated
	Disabled
	// Available is not enabled, and Steam has a matching copy.
	Available
	// SteamMismatch is a Workshop mod whose Steam files are another version.
	SteamMismatch
	NotSubscribed
	// Missing is an own mod that the player must download.
	Missing

	disabledSuffix = ".disabled"
	partialSuffix  = ".tmp"
	// partialAge leaves a younger partial alone, because another tlmp-manager can still write it.
	partialAge = time.Hour
	promodsURL = "https://promods.net/setup.php?game=ets"
)

var (
	ErrNotListed = errors.New("not on the TrucklineMP mod list")
	ErrMismatch  = errors.New("not the version on the TrucklineMP mod list")
	errNoCopy    = errors.New("no matching copy to enable")
)

func New(o Opts) *Mods {
	m := &Mods{
		allowlistURL: o.AllowlistURL,
		detailsURL:   o.DetailsURL,
		hashes:       o.Hashes,
		httpClient:   &http.Client{},
	}
	if m.detailsURL == "" {
		m.detailsURL = DetailsURL
	}
	if m.hashes == nil {
		m.hashes = &state.Hashes{Entries: map[string]state.Hash{}}
	}
	return m
}

// ModDir is the folder that the core mounts optional mods from.
func ModDir(p steam.Paths) string {
	return filepath.Join(steam.DocumentsDir(p.PrefixDir), "TrucklineMP", "mod")
}

// SteamURL returns a URL that opens the Workshop page in the Steam client, where the player can subscribe.
func (e Entry) SteamURL() string {
	return "steam://url/CommunityFilePage/" + e.WorkshopID
}

func (e Entry) WorkshopURL() string {
	return "https://steamcommunity.com/sharedfiles/filedetails/?id=" + e.WorkshopID
}

// DownloadURL returns "" when the file name does not tell where to get an own mod.
func (e Entry) DownloadURL() string {
	if strings.HasPrefix(strings.ToLower(e.Name), "promods-eu") {
		return promodsURL
	}
	return ""
}

func (m *Mods) Check(ctx context.Context, p steam.Paths) (*Report, error) {
	r, err := m.report(ctx, p)
	if err != nil {
		return nil, err
	}
	for i := range r.Mods {
		r.Mods[i] = m.check(r.Mods[i])
	}
	r.Leftovers = leftovers(r)
	m.saveHashes()
	return r, nil
}

// Refresh checks one mod again, for example while Steam downloads it.
func (m *Mods) Refresh(mod Mod) Mod {
	mod = m.check(mod)
	m.saveHashes()
	return mod
}

// Find accepts a listed file name or a Workshop id, in any case.
func (r *Report) Find(arg string) (Mod, bool) {
	for _, mod := range r.Mods {
		if strings.EqualFold(mod.Name, arg) || (mod.WorkshopID != "" && mod.WorkshopID == arg) {
			return mod, true
		}
	}
	return Mod{}, false
}

// Enable copies or packs the Steam copy, or renames a disabled copy back.
func (m *Mods) Enable(ctx context.Context, mod Mod) error {
	switch {
	case mod.State == Enabled:
		return nil
	case mod.State == Disabled:
		if err := os.Rename(mod.dst+disabledSuffix, mod.dst); err != nil {
			return err
		}
		m.remember(mod.dst, mod.MD5)
		return nil
	case mod.source.folder:
		return m.install(ctx, mod.dst, mod.MD5, func(w io.Writer) error {
			return packFolder(w, os.DirFS(mod.source.path))
		})
	case mod.source.path != "":
		return m.install(ctx, mod.dst, mod.MD5, func(w io.Writer) error {
			return copyFile(w, mod.source.path)
		})
	default:
		return fmt.Errorf("%s: %w", mod.Name, errNoCopy)
	}
}

// Disable deletes a copy that Steam can supply again, else it keeps the file as <name>.disabled.
func (m *Mods) Disable(mod Mod) (kept string, err error) {
	if _, err := os.Stat(mod.dst); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if mod.SteamCopy {
		return "", os.Remove(mod.dst)
	}
	kept = mod.dst + disabledSuffix
	return kept, os.Rename(mod.dst, kept)
}

// Remove deletes a listed mod or a leftover, and its disabled copy.
func (m *Mods) Remove(r *Report, name string) error {
	var dst string
	if mod, ok := r.Find(name); ok {
		dst = mod.dst
	}
	for _, l := range r.Leftovers {
		if strings.EqualFold(l, name) {
			dst = filepath.Join(r.Dir, l)
		}
	}
	if dst == "" {
		return fmt.Errorf("%s: %w", name, ErrNotListed)
	}

	var errs []error
	for _, p := range []string{dst, dst + disabledSuffix} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// RemoveAll deletes every mod file, disabled copy, and partial in the mod folder, and keeps other files.
func (m *Mods) RemoveAll(p steam.Paths) error {
	dir := ModDir(p)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		n := e.Name()
		if !e.Type().IsRegular() || !validName(strings.TrimSuffix(n, disabledSuffix)) && !isPartial(n) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	// The folder stays when it holds other files.
	_ = os.Remove(dir)
	return errors.Join(errs...)
}

// Add copies an own mod that the player downloaded, and saves it under the listed name.
func (m *Mods) Add(ctx context.Context, r *Report, file string) (Mod, error) {
	base := filepath.Base(file)
	i := -1
	for j, mod := range r.Mods {
		if strings.EqualFold(mod.Name, base) {
			i = j
		}
	}
	if i < 0 {
		return Mod{}, fmt.Errorf("%s: %w", base, ErrNotListed)
	}
	mod := r.Mods[i]

	if err := m.install(ctx, mod.dst, mod.MD5, func(w io.Writer) error { return copyFile(w, file) }); err != nil {
		return mod, err
	}
	if err := os.Remove(mod.dst + disabledSuffix); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("Could not delete the disabled copy", "file", mod.Name, "err", err)
	}
	mod.State = Enabled
	return mod, nil
}

// Sync updates enabled Workshop mods from Steam and returns the mods that stay outdated.
// It never enables a mod.
func (m *Mods) Sync(ctx context.Context, p steam.Paths) ([]Mod, error) {
	r, err := m.report(ctx, p)
	if err != nil {
		return nil, err
	}
	defer m.saveHashes()

	var outdated []Mod
	for _, mod := range r.Mods {
		if _, err := os.Stat(mod.dst); err != nil {
			continue
		}
		if mod = m.check(mod); mod.State != Outdated {
			continue
		}
		if mod.SteamCopy {
			err := m.Enable(ctx, mod)
			if err == nil {
				slog.Info("Optional mod updated", "file", mod.Name)
				continue
			}
			slog.Warn("Could not update the optional mod", "file", mod.Name, "err", err)
		}
		outdated = append(outdated, mod)
	}
	return outdated, ctx.Err()
}

// report fetches the allowlist and resolves the paths. It does not read the mod files.
func (m *Mods) report(ctx context.Context, p steam.Paths) (*Report, error) {
	entries, err := m.fetchAllowlist(ctx)
	if err != nil {
		return nil, err
	}
	workshop, err := steam.WorkshopDir(p.GameDir)
	if err != nil {
		slog.Warn("Steam Workshop mods are not available", "err", err)
	}

	r := &Report{Dir: ModDir(p), Mods: make([]Mod, 0, len(entries))}
	for _, e := range entries {
		mod := Mod{Entry: e, dst: filepath.Join(r.Dir, e.Name)}
		if e.WorkshopID != "" && workshop != "" {
			mod.itemDir = filepath.Join(workshop, e.WorkshopID)
		}
		r.Mods = append(r.Mods, mod)
	}
	return r, nil
}

func (m *Mods) check(mod Mod) Mod {
	steamCopy := copyNone
	mod.source = source{}
	if mod.itemDir != "" {
		mod.source, steamCopy = m.findCopy(mod.itemDir, mod.MD5)
	}
	mod.SteamCopy = steamCopy == copyFound

	sum, err := m.fileMD5(mod.dst)
	switch {
	case err == nil && sum == mod.MD5:
		mod.State = Enabled
	case err == nil:
		mod.State = Outdated
	case m.matches(mod.dst+disabledSuffix, mod.MD5):
		mod.State = Disabled
	case steamCopy == copyFound:
		mod.State = Available
	case steamCopy == copyMismatch:
		mod.State = SteamMismatch
	case mod.WorkshopID != "":
		mod.State = NotSubscribed
	default:
		mod.State = Missing
	}
	return mod
}

func (m *Mods) matches(path, want string) bool {
	sum, err := m.fileMD5(path)
	return err == nil && sum == want
}

// leftovers lists the mod files and disabled copies whose names are not on the list.
func leftovers(r *Report) []string {
	listed := make(map[string]bool, len(r.Mods))
	for _, mod := range r.Mods {
		listed[strings.ToLower(mod.Name)] = true
	}
	entries, _ := os.ReadDir(r.Dir)
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		n := strings.TrimSuffix(e.Name(), disabledSuffix)
		if !e.Type().IsRegular() || !validName(n) || listed[strings.ToLower(n)] || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// install writes beside dst and renames only verified bytes, so a failure keeps the previous file.
func (m *Mods) install(ctx context.Context, dst, want string, write func(io.Writer) error) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	removeOldPartials(dir)

	f, err := os.CreateTemp(dir, filepath.Base(dst)+".*"+partialSuffix)
	if err != nil {
		return err
	}
	h := md5.New()
	err = write(ctxWriter{ctx: ctx, w: io.MultiWriter(f, h)})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if got := hex.EncodeToString(h.Sum(nil)); err == nil && got != want {
		err = fmt.Errorf("%s: %w: MD5 %s, want %s", filepath.Base(dst), ErrMismatch, got, want)
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(f.Name(), dst)
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	m.remember(dst, want)
	return nil
}

// remember records an installed file, so the next check does not read it again.
func (m *Mods) remember(path, sum string) {
	if fi, err := os.Stat(path); err == nil {
		m.hashes.Entries[path] = state.Hash{Sig: fileSig(fi), MD5: sum}
	}
	m.saveHashes()
}

func (m *Mods) saveHashes() {
	if err := state.SaveHashes(m.hashes); err != nil {
		slog.Warn("Could not save the mod hash cache", "err", err)
	}
}

func removeOldPartials(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && isPartial(e.Name()) && time.Since(fi.ModTime()) > partialAge {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// isPartial matches the temp names of install: <mod name>.<random>.tmp.
func isPartial(name string) bool {
	stem, ok := strings.CutSuffix(name, partialSuffix)
	i := strings.LastIndexByte(stem, '.')
	return ok && i > 0 && validName(stem[:i])
}

func copyFile(w io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}

func (c ctxWriter) Write(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.w.Write(p)
}

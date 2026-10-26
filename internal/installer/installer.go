package installer

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kamikazechaser/tlmp-manager/internal/config"
	"github.com/kamikazechaser/tlmp-manager/internal/shim"
	"github.com/kamikazechaser/tlmp-manager/internal/state"
	"github.com/kamikazechaser/tlmp-manager/internal/steam"
)

type (
	Opts struct {
		URLs config.URLs
	}

	Installer struct {
		urls       config.URLs
		st         *state.State
		httpClient *http.Client
	}

	file struct {
		name, dst, hash string
		ok              bool
	}

	// Progress reports a running update. FileFrac is -1 when the server sends no file size.
	Progress struct {
		File     string
		Done     int
		Total    int
		FileFrac float64
		Bytes    int64
	}

	countingReader struct {
		r      io.Reader
		n      int64
		size   int64
		report func(n, size int64)
	}
)

const (
	dstorageFile       = "dstorage.dll"
	dstorageOGFile     = "dstorage_og.dll"
	dstorageLegacyFile = "dstorage_tlmp.dll"
	// coreDLL must match CORE_DLL in shim/dstorage.c.
	coreDLL    = "TrucklineMP/trucklineets.dll"
	managedDir = "TrucklineMP"
)

var (
	ErrUnsafePath = errors.New("manifest path outside the TrucklineMP directory")
)

func New(o Opts) (*Installer, error) {
	st, err := state.Load()
	if err != nil {
		return nil, err
	}
	return &Installer{
		urls:       o.URLs,
		st:         st,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

// Run installs the missing and stale files and the shim. onProgress can be nil.
func (i *Installer) Run(ctx context.Context, paths steam.Paths, onProgress func(Progress)) (Progress, error) {
	m, err := i.fetchManifest(ctx)
	if err != nil {
		return Progress{}, err
	}

	for name := range i.st.Installed {
		if _, ok := m[name]; !ok {
			delete(i.st.Installed, name)
		}
	}
	var downloads []file
	for _, f := range i.diff(m, paths.PrefixDir) {
		if !f.ok {
			downloads = append(downloads, f)
		}
	}

	p := Progress{Total: len(downloads)}
	for _, f := range downloads {
		slog.Debug("Downloading", "file", f.name)
		p.File = f.name
		before := p.Bytes
		report := func(n, size int64) {
			p.Bytes, p.FileFrac = before+n, -1
			if size > 0 {
				p.FileFrac = float64(n) / float64(size)
			}
			if onProgress != nil {
				onProgress(p)
			}
		}
		if err := i.download(ctx, i.urls.Files+f.name, f.dst, f.hash, report); err != nil {
			return p, fmt.Errorf("installing %s: %w", f.name, err)
		}
		i.st.Installed[f.name] = f.hash
		p.Done++
	}

	if !shimInstalled(paths.GameDir) {
		if err := installShim(paths.GameDir); err != nil {
			return p, fmt.Errorf("installing %s: %w", dstorageFile, err)
		}
	}
	return p, state.Save(i.st)
}

// Check returns the files that Run would install, and the number of files that TLMP has.
func (i *Installer) Check(ctx context.Context, p steam.Paths) (pending []string, total int, err error) {
	m, err := i.fetchManifest(ctx)
	if err != nil {
		return nil, 0, err
	}
	for _, f := range i.diff(m, p.PrefixDir) {
		if !f.ok {
			pending = append(pending, f.name)
		}
	}
	if !shimInstalled(p.GameDir) {
		pending = append(pending, dstorageFile)
	}
	return pending, len(m) + 1, nil
}

// Disable puts back the dstorage.dll that the shim replaced and keeps the downloaded files.
func (i *Installer) Disable(p steam.Paths) error {
	dstorPath := filepath.Join(p.GameDir, dstorageFile)
	ogPath := filepath.Join(p.GameDir, dstorageOGFile)
	var errs []error
	// An install that stopped before it wrote the shim leaves the backup as the only copy.
	_, statErr := os.Stat(dstorPath)
	if shimInstalled(p.GameDir) || errors.Is(statErr, os.ErrNotExist) {
		err := os.Rename(ogPath, dstorPath)
		if errors.Is(err, os.ErrNotExist) {
			err = os.Remove(dstorPath)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("restoring %s: %w", dstorageFile, err))
		}
	} else if err := os.Remove(ogPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}
	if err := os.Remove(filepath.Join(p.GameDir, dstorageLegacyFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (i *Installer) Installed(p steam.Paths) bool {
	_, err := os.Stat(destPath(coreDLL, p.PrefixDir))
	return err == nil && shimInstalled(p.GameDir)
}

// Uninstall disables the shim and removes the installed files and the state.
// The error lists every effect that remains.
func (i *Installer) Uninstall(p steam.Paths) error {
	var errs []error
	remove := func(p string) {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}

	if err := i.Disable(p); err != nil {
		errs = append(errs, err)
	}
	for name := range i.st.Installed {
		if managed(name) {
			remove(destPath(name, p.PrefixDir))
		}
	}
	if err := os.RemoveAll(destPath(managedDir+"/data", p.PrefixDir)); err != nil {
		errs = append(errs, err)
	}
	remove(destPath(coreDLL, p.PrefixDir))

	if err := state.Save(&state.State{Installed: map[string]string{}}); err != nil {
		errs = append(errs, fmt.Errorf("clearing state: %w", err))
	}
	return errors.Join(errs...)
}

func (i *Installer) fetchManifest(ctx context.Context) (map[string]string, error) {
	body, _, err := i.get(ctx, i.urls.Manifest)
	if err != nil {
		return nil, fmt.Errorf("fetching manifest: %w", err)
	}
	defer func() { _ = body.Close() }()

	var m map[string]string
	if err := json.NewDecoder(body).Decode(&m); err != nil {
		return nil, fmt.Errorf("decoding manifest: %w", err)
	}
	for name := range m {
		if !managed(name) {
			return nil, fmt.Errorf("%w: %q", ErrUnsafePath, name)
		}
	}
	return m, nil
}

// diff trusts the state cache when the file exists, else it hashes the file.
func (i *Installer) diff(m map[string]string, prefixDir string) []file {
	files := make([]file, 0, len(m))
	for name, hash := range m {
		f := file{name: name, dst: destPath(name, prefixDir), hash: hash}
		_, err := os.Stat(f.dst)
		f.ok = err == nil && i.st.Installed[name] == hash
		if !f.ok {
			got, _ := fileMD5(f.dst)
			if f.ok = got == hash; f.ok {
				i.st.Installed[name] = hash
			}
		}
		files = append(files, f)
	}
	sort.Slice(files, func(a, b int) bool { return files[a].name < files[b].name })
	return files
}

func (i *Installer) get(ctx context.Context, url string) (io.ReadCloser, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := i.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, 0, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return resp.Body, resp.ContentLength, nil
}

func (i *Installer) download(ctx context.Context, url, dst, hash string, report func(n, size int64)) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	body, size, err := i.get(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()

	tmp := dst + ".tmp"
	if err := writeVerified(&countingReader{r: body, size: size, report: report}, tmp, hash); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	c.report(c.n, c.size)
	return n, err
}

func managed(name string) bool {
	return filepath.IsLocal(name) && strings.HasPrefix(path.Clean(name), managedDir+"/")
}

func destPath(name, prefixDir string) string {
	return filepath.Join(prefixDir, "drive_c", "ProgramData", filepath.FromSlash(name))
}

func shimInstalled(gameDir string) bool {
	got, _ := fileMD5(filepath.Join(gameDir, dstorageFile))
	return got == shim.MD5
}

// installShim keeps the current dstorage.dll, whatever it is, as dstorage_og.dll, because the shim forwards to it.
func installShim(gameDir string) error {
	dstorPath := filepath.Join(gameDir, dstorageFile)
	if err := os.Rename(dstorPath, filepath.Join(gameDir, dstorageOGFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(filepath.Join(gameDir, dstorageLegacyFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("Could not remove the old TLMP dll", "file", dstorageLegacyFile, "err", err)
	}
	return writeFile(dstorPath, shim.DLL)
}

func writeFile(dst string, b []byte) error {
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func fileMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeVerified(r io.Reader, dst, hash string) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	h := md5.New()
	if _, err := io.Copy(f, io.TeeReader(r, h)); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != hash {
		return fmt.Errorf("hash mismatch: got %s, want %s", got, hash)
	}
	return nil
}

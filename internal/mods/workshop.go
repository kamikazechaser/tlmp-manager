package mods

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kamikazechaser/tlmp-manager/internal/state"
)

type (
	// source is the Steam copy that matches the allowlist: an archive, or a package folder to pack.
	source struct {
		path   string
		folder bool
	}

	copyState int
)

const (
	copyNone copyState = iota
	copyMismatch
	copyFound

	// maxArchiveDepth matches the official launcher: item/a/b/c/file.scs is the deepest archive.
	maxArchiveDepth = 3
)

var (
	packageName = regexp.MustCompile(`package_name\s*:\s*"([^"]*)"`)
)

// findCopy tries package folders only when the item has no archives, as the official launcher does.
func (m *Mods) findCopy(itemDir, want string) (source, copyState) {
	archives := findArchives(itemDir)
	for _, a := range archives {
		if sum, err := m.fileMD5(a); err == nil && sum == want {
			return source{path: a}, copyFound
		}
	}
	if len(archives) > 0 {
		return source{}, copyMismatch
	}

	folders := packageFolders(itemDir)
	for _, d := range folders {
		if sum, err := m.folderMD5(d); err == nil && sum == want {
			return source{path: d, folder: true}, copyFound
		}
	}
	if len(folders) > 0 {
		return source{}, copyMismatch
	}
	return source{}, copyNone
}

func findArchives(itemDir string) []string {
	var out []string
	_ = filepath.WalkDir(itemDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(itemDir, p)
			if p != itemDir && strings.Count(rel, string(filepath.Separator)) >= maxArchiveDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && isArchive(d.Name()) {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// packageFolders returns the folders that versions.sii names, then universal.
func packageFolders(itemDir string) []string {
	b, _ := os.ReadFile(filepath.Join(itemDir, "versions.sii"))
	var names []string
	for _, match := range packageName.FindAllSubmatch(b, -1) {
		names = append(names, string(match[1]))
	}
	names = append(names, "universal")

	seen := make(map[string]bool, len(names))
	var out []string
	for _, n := range names {
		if seen[n] || n == "" || n == "." || n == ".." || strings.ContainsAny(n, `/\`) {
			continue
		}
		seen[n] = true
		p := filepath.Join(itemDir, n)
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

func isArchive(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".scs") || strings.HasSuffix(lower, ".zip")
}

// fileMD5 trusts the cache while the size and the modification time are unchanged.
func (m *Mods) fileMD5(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	sig := fileSig(fi)
	if h, ok := m.hashes.Entries[path]; ok && h.Sig == sig {
		return h.MD5, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	m.hashes.Entries[path] = state.Hash{Sig: sig, MD5: sum}
	return sum, nil
}

// folderMD5 trusts the cache while no file in the folder changes its path, size, or time.
func (m *Mods) folderMD5(dir string) (string, error) {
	fsys := os.DirFS(dir)
	files, err := packFiles(fsys)
	if err != nil {
		return "", err
	}
	s := sha1.New()
	for _, f := range files {
		_, _ = io.WriteString(s, f.path+"|"+strconv.FormatInt(f.size, 10)+"|"+strconv.FormatInt(f.modTime.UnixNano(), 10)+"\n")
	}
	sig := hex.EncodeToString(s.Sum(nil))
	if h, ok := m.hashes.Entries[dir]; ok && h.Sig == sig {
		return h.MD5, nil
	}

	h := md5.New()
	if err := packFolder(h, fsys); err != nil {
		return "", err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	m.hashes.Entries[dir] = state.Hash{Sig: sig, MD5: sum}
	return sum, nil
}

func fileSig(fi fs.FileInfo) string {
	return strconv.FormatInt(fi.Size(), 10) + "|" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
}

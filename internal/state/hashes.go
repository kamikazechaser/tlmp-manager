package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type (
	// Hashes caches the MD5 sums of mod files and packed folders by absolute path.
	Hashes struct {
		Entries map[string]Hash `koanf:"hashes"`
	}

	// Hash stays valid while Sig, which the caller derives from sizes and times, is unchanged.
	Hash struct {
		Sig string `koanf:"sig" toml:"sig"`
		MD5 string `koanf:"md5" toml:"md5"`
	}
)

// HashesPath is apart from Path, because the installer and the mods cache save independently.
func HashesPath() string {
	return filepath.Join(Dir(), "hashes.toml")
}

func LoadHashes() (*Hashes, error) {
	p := HashesPath()
	h := &Hashes{Entries: make(map[string]Hash)}

	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return h, nil
	}

	k := koanf.New(".")
	if err := k.Load(file.Provider(p), toml.Parser()); err != nil {
		return nil, fmt.Errorf("loading hashes: %w", err)
	}
	if err := k.UnmarshalWithConf("", h, koanf.UnmarshalConf{Tag: "koanf"}); err != nil {
		return nil, fmt.Errorf("parsing hashes: %w", err)
	}
	if h.Entries == nil {
		h.Entries = make(map[string]Hash)
	}
	return h, nil
}

// SaveHashes drops the entries whose path no longer exists.
func SaveHashes(h *Hashes) error {
	for p := range h.Entries {
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			delete(h.Entries, p)
		}
	}

	d := Dir()
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	k := koanf.New(".")
	if err := k.Set("hashes", h.Entries); err != nil {
		return err
	}
	b, err := k.Marshal(toml.Parser())
	if err != nil {
		return fmt.Errorf("marshaling hashes: %w", err)
	}

	// A launch and a mods command can save at the same time, so each one uses its own temp file.
	f, err := os.CreateTemp(d, "hashes.*.tmp")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), HashesPath())
	}
	if err != nil {
		_ = os.Remove(f.Name())
	}
	return err
}

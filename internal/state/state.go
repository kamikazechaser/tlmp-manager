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
	// State caches the md5 hashes of installed files.
	State struct {
		Installed map[string]string `koanf:"installed"`
	}
)

func Dir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "tlmp-manager")
}

func Path() string {
	return filepath.Join(Dir(), "state.toml")
}

func LogDir() string {
	return filepath.Join(Dir(), "logs")
}

func Load() (*State, error) {
	p := Path()
	st := &State{Installed: make(map[string]string)}

	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return st, nil
	}

	k := koanf.New(".")
	if err := k.Load(file.Provider(p), toml.Parser()); err != nil {
		return nil, fmt.Errorf("loading state: %w", err)
	}
	if err := k.UnmarshalWithConf("", st, koanf.UnmarshalConf{Tag: "koanf"}); err != nil {
		return nil, fmt.Errorf("parsing state: %w", err)
	}
	if st.Installed == nil {
		st.Installed = make(map[string]string)
	}
	return st, nil
}

func Save(st *State) error {
	d := Dir()
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}

	k := koanf.New(".")
	if err := k.Set("installed", st.Installed); err != nil {
		return err
	}

	b, err := k.Marshal(toml.Parser())
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	p := Path()
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

const (
	manifestSuffix = "/latest.json"
	filesSuffix    = "/files/"
	modsSuffix     = "/api/public/mods"
)

type (
	Config struct {
		GameDir   string `koanf:"game_dir"   toml:"game_dir"`
		PrefixDir string `koanf:"prefix_dir" toml:"prefix_dir"`
	}

	URLs struct {
		Manifest string
		Files    string
	}
)

func NewURLs(cdnBase string) URLs {
	return URLs{
		Manifest: cdnBase + manifestSuffix,
		Files:    cdnBase + filesSuffix,
	}
}

// ModsURL returns the optional mod allowlist on the game API, which the core also reads.
func ModsURL(gameBase string) string {
	return gameBase + modsSuffix
}

func Path() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "tlmp-manager", "config.toml")
}

// Load returns an empty Config when no config file exists.
func Load() (*Config, error) {
	p := Path()
	cfg := &Config{}

	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}

	k := koanf.New(".")
	if err := k.Load(file.Provider(p), toml.Parser()); err != nil {
		return nil, fmt.Errorf("loading config %s: %w", p, err)
	}
	if err := k.UnmarshalWithConf("", cfg, koanf.UnmarshalConf{Tag: "koanf"}); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}

func Save(cfg *Config) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	k := koanf.New(".")
	if err := k.Set("game_dir", cfg.GameDir); err != nil {
		return err
	}
	if err := k.Set("prefix_dir", cfg.PrefixDir); err != nil {
		return err
	}

	b, err := k.Marshal(toml.Parser())
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

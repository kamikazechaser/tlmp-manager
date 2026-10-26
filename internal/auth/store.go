package auth

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

type (
	SessionsOpts struct {
		// Dir holds the fallback file for a machine without a keyring.
		Dir string
	}

	// Sessions keeps the long-lived session ID in the keyring, or else in a 0600 file.
	Sessions struct {
		file string
	}
)

const (
	keyringService = "tlmp-manager"
	keyringUser    = "session"
)

var (
	ErrNotLoggedIn = errors.New("not logged in; run 'tlmp-manager login'")
)

func NewSessions(o SessionsOpts) *Sessions {
	return &Sessions{file: filepath.Join(o.Dir, "session")}
}

func (s *Sessions) Load() (string, error) {
	id, err := keyring.Get(keyringService, keyringUser)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		slog.Debug("System keyring unavailable", "err", err)
	}

	b, err := os.ReadFile(s.file)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotLoggedIn
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (s *Sessions) Save(id string) error {
	err := keyring.Set(keyringService, keyringUser, id)
	if err == nil {
		// An older fallback file would outlive a logout from the keyring.
		if err := os.Remove(s.file); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("Could not remove the old session file", "path", s.file, "err", err)
		}
		return nil
	}
	slog.Warn("System keyring unavailable; storing the session unencrypted", "path", s.file, "err", err)

	if err := os.MkdirAll(filepath.Dir(s.file), 0o755); err != nil {
		return err
	}
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, []byte(id), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.file)
}

func (s *Sessions) Delete() error {
	if err := keyring.Delete(keyringService, keyringUser); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		slog.Debug("System keyring unavailable", "err", err)
	}
	if err := os.Remove(s.file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

package steam

import (
	"errors"
	"path/filepath"
	"strings"
)

const (
	// workshopDepth matches the official launcher: the game directory and five parents.
	workshopDepth = 6
)

var (
	ErrNotInLibrary = errors.New("the game directory is not inside a Steam library")
)

// WorkshopDir returns the Workshop content directory of ETS2. Steam keeps it in the library of the game.
func WorkshopDir(gameDir string) (string, error) {
	if !filepath.IsAbs(gameDir) {
		return "", ErrNotInLibrary
	}
	dir := filepath.Clean(gameDir)
	for range workshopDepth {
		if strings.EqualFold(filepath.Base(dir), "steamapps") {
			return filepath.Join(dir, "workshop", "content", appID), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", ErrNotInLibrary
}

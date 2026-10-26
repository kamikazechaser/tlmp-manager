package mods

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type (
	// Entry is one mod on the allowlist. The core mounts a file only when its name is on the list.
	Entry struct {
		Name string
		MD5  string
		// WorkshopID is empty for an own mod, which the player gets outside Steam.
		WorkshopID string
	}

	rawEntry struct {
		Name string `json:"name"`
		MD5  string `json:"md5"`
	}
)

const (
	allowlistTimeout = 8 * time.Second
	maxNameLen       = 200
)

func (m *Mods) fetchAllowlist(ctx context.Context) ([]Entry, error) {
	ctx, cancel := context.WithTimeout(ctx, allowlistTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.allowlistURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching the mod list: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching the mod list: HTTP %d", resp.StatusCode)
	}

	var body struct {
		Mods []rawEntry `json:"mods"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding the mod list: %w", err)
	}
	return parseAllowlist(body.Mods), nil
}

// parseAllowlist keeps the entries that the official launcher keeps, and only the ETS2 ones.
func parseAllowlist(raw []rawEntry) []Entry {
	seen := make(map[string]bool, len(raw))
	var out []Entry
	for _, r := range raw {
		lower := strings.ToLower(r.Name)
		if !validName(r.Name) || !validMD5(r.MD5) || seen[lower] {
			continue
		}
		seen[lower] = true
		if strings.HasPrefix(lower, "ats-") {
			continue
		}
		out = append(out, Entry{Name: r.Name, MD5: strings.ToLower(r.MD5), WorkshopID: workshopID(r.Name)})
	}
	return out
}

// validName accepts a single .scs or .zip file name that Windows can store.
func validName(name string) bool {
	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, ".scs") && !strings.HasSuffix(lower, ".zip") {
		return false
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool {
		return r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r)
	})
}

func validMD5(s string) bool {
	_, err := hex.DecodeString(s)
	return len(s) == 32 && err == nil
}

// workshopID returns the digits of a name such as 713338424.scs, or "" for an own mod.
func workshopID(name string) string {
	stem := name[:len(name)-len(".scs")]
	if stem == "" || strings.ContainsFunc(stem, func(r rune) bool { return r < '0' || r > '9' }) {
		return ""
	}
	return stem
}

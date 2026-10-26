package mods

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	// DetailsURL is the Steam Web API for public Workshop items. It needs no key.
	DetailsURL     = "https://api.steampowered.com/ISteamRemoteStorage/GetPublishedFileDetails/v1/"
	detailsBatch   = 100
	detailsTimeout = 10 * time.Second
)

// Titles returns display names by file name. A failed Steam request keeps the file names.
func (m *Mods) Titles(ctx context.Context, mods []Mod) map[string]string {
	titles := make(map[string]string, len(mods))
	var ids []string
	for _, mod := range mods {
		titles[mod.Name] = localTitle(mod.Name)
		if mod.WorkshopID != "" {
			ids = append(ids, mod.WorkshopID)
		}
	}

	steam := make(map[string]string, len(ids))
	for batch := range slices.Chunk(ids, detailsBatch) {
		if err := m.fetchTitles(ctx, batch, steam); err != nil {
			slog.Debug("Steam Workshop titles are not available", "err", err)
			break
		}
	}
	for _, mod := range mods {
		if t := steam[mod.WorkshopID]; t != "" {
			titles[mod.Name] = t
		}
	}
	return titles
}

func (m *Mods) fetchTitles(ctx context.Context, ids []string, out map[string]string) error {
	ctx, cancel := context.WithTimeout(ctx, detailsTimeout)
	defer cancel()
	form := url.Values{"itemcount": {strconv.Itoa(len(ids))}}
	for i, id := range ids {
		form.Set(fmt.Sprintf("publishedfileids[%d]", i), id)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.detailsURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from Steam", resp.StatusCode)
	}

	var body struct {
		Response struct {
			Details []struct {
				ID     string `json:"publishedfileid"`
				Result int    `json:"result"`
				Title  string `json:"title"`
			} `json:"publishedfiledetails"`
		} `json:"response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
		return err
	}
	if body.Response.Details == nil {
		return errors.New("no publishedfiledetails from Steam")
	}
	for _, d := range body.Response.Details {
		if d.Result == 1 {
			out[d.ID] = strings.TrimSpace(d.Title)
		}
	}
	return nil
}

// localTitle shortens a ProMods name as the official launcher does: promods-me-assets-v284.scs is ME Assets V284.
func localTitle(name string) string {
	rest, ok := strings.CutPrefix(strings.ToLower(name), "promods-")
	region, rest, found := strings.Cut(rest, "-")
	if !ok || !found || region == "" {
		return name
	}
	pieces := strings.FieldsFunc(rest[:len(rest)-len(".scs")], func(r rune) bool {
		return r == '-' || r == '_' || unicode.IsSpace(r)
	})
	if len(pieces) == 0 {
		return name
	}
	for i, p := range pieces {
		r := []rune(p)
		pieces[i] = string(unicode.ToUpper(r[0])) + string(r[1:])
	}
	if region != "eu" {
		pieces = append([]string{strings.ToUpper(region)}, pieces...)
	}
	return strings.Join(pieces, " ")
}

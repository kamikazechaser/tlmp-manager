package mods

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestParseAllowlist(t *testing.T) {
	const sum = "11b11dc9739766125f216c1af8c411c8"
	raw := []rawEntry{
		{Name: "713338424.scs", MD5: sum},
		{Name: "1787041596.ZIP", MD5: strings.ToUpper(sum)},
		{Name: "promods-eu-map-v284.scs", MD5: sum},
		{Name: "zzPNG_StreetLamps_[FreeAddon].scs", MD5: sum},
		{Name: "ats-2942953789.zip", MD5: sum},
		{Name: "713338424.SCS", MD5: sum},
		{Name: "readme.txt", MD5: sum},
		{Name: "sub/mod.scs", MD5: sum},
		{Name: `sub\mod.scs`, MD5: sum},
		{Name: "what?.scs", MD5: sum},
		{Name: "tab\t.scs", MD5: sum},
		{Name: strings.Repeat("a", 197) + ".scs", MD5: sum},
		{Name: "short-md5.scs", MD5: sum[:31]},
		{Name: "not-hex.scs", MD5: strings.Repeat("g", 32)},
	}
	want := []Entry{
		{Name: "713338424.scs", MD5: sum, WorkshopID: "713338424"},
		{Name: "1787041596.ZIP", MD5: sum, WorkshopID: "1787041596"},
		{Name: "promods-eu-map-v284.scs", MD5: sum},
		{Name: "zzPNG_StreetLamps_[FreeAddon].scs", MD5: sum},
	}
	if got := parseAllowlist(raw); !slices.Equal(got, want) {
		t.Errorf("parseAllowlist() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestFetchAllowlist(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		code    int
		wantErr bool
	}{
		{name: "list", body: `{"mods":[{"name":"713338424.scs","md5":"11b11dc9739766125f216c1af8c411c8"}]}`, code: http.StatusOK},
		{name: "server error", body: `{}`, code: http.StatusBadGateway, wantErr: true},
		{name: "bad body", body: `<html>`, code: http.StatusOK, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.code)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(srv.Close)

			m := New(Opts{AllowlistURL: srv.URL})
			got, err := m.fetchAllowlist(context.Background())
			if (err != nil) != tt.wantErr || (err == nil && len(got) != 1) {
				t.Errorf("fetchAllowlist() = %+v, %v; want error: %v", got, err, tt.wantErr)
			}
		})
	}
}

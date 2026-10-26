package mods

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestTitles(t *testing.T) {
	mods := []Mod{
		{Name: "713338424.scs", WorkshopID: "713338424"},
		{Name: "999.zip", WorkshopID: "999"},
		{Name: "promods-eu-assets-v284.scs"},
		{Name: "promods-me-assets-v284.scs"},
		{Name: "promods-eu-dlcsupport-v284.zip"},
		{Name: "PNG_PE_BASE_1.scs"},
	}
	tests := []struct {
		name  string
		down  bool
		want0 string
	}{
		{name: "Steam answers", want0: "Realistic Truck Physics Mod v9.2"},
		{name: "Steam down", down: true, want0: "713338424.scs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var form string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.down {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_ = r.ParseForm()
				form = r.PostForm.Get("itemcount") + " " + r.PostForm.Get("publishedfileids[0]") + " " + r.PostForm.Get("publishedfileids[1]")
				_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"publishedfiledetails": []map[string]any{
					{"publishedfileid": "713338424", "result": 1, "title": " Realistic Truck Physics Mod v9.2 "},
					{"publishedfileid": "999", "result": 9},
				}}})
			}))
			t.Cleanup(srv.Close)

			got := New(Opts{DetailsURL: srv.URL}).Titles(context.Background(), mods)
			want := map[string]string{
				"713338424.scs":                  tt.want0,
				"999.zip":                        "999.zip",
				"promods-eu-assets-v284.scs":     "Assets V284",
				"promods-me-assets-v284.scs":     "ME Assets V284",
				"promods-eu-dlcsupport-v284.zip": "Dlcsupport V284",
				"PNG_PE_BASE_1.scs":              "PNG_PE_BASE_1.scs",
			}
			for name, w := range want {
				if got[name] != w {
					t.Errorf("Titles()[%s] = %q, want %q", name, got[name], w)
				}
			}
			if !tt.down && form != "2 713338424 999" {
				t.Errorf("Steam form = %q, want 2 ids", form)
			}
		})
	}
}

func TestTitlesBatches(t *testing.T) {
	var batches []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		n, _ := strconv.Atoi(r.PostForm.Get("itemcount"))
		batches = append(batches, n)
		_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"publishedfiledetails": []any{}}})
	}))
	t.Cleanup(srv.Close)

	mods := make([]Mod, 150)
	for i := range mods {
		id := strconv.Itoa(1000 + i)
		mods[i] = Mod{Name: id + ".scs", WorkshopID: id}
	}
	New(Opts{DetailsURL: srv.URL}).Titles(context.Background(), mods)
	if len(batches) != 2 || batches[0] != 100 || batches[1] != 50 {
		t.Errorf("batches = %v, want [100 50]", batches)
	}
}

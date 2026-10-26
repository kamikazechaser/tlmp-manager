package steam

import (
	"errors"
	"testing"
)

func TestWorkshopDir(t *testing.T) {
	tests := []struct {
		name, gameDir, want string
	}{
		{name: "game in a library", gameDir: "/lib/steamapps/common/Euro Truck Simulator 2/bin/win_x64", want: "/lib/steamapps/workshop/content/227300"},
		{name: "case of steamapps", gameDir: "/lib/SteamApps/common/ETS2/bin/win_x64", want: "/lib/SteamApps/workshop/content/227300"},
		{name: "six directories", gameDir: "/lib/steamapps/a/b/c/d/e", want: "/lib/steamapps/workshop/content/227300"},
		{name: "seven directories", gameDir: "/lib/steamapps/a/b/c/d/e/f"},
		{name: "not in a library", gameDir: "/games/ets2/bin/win_x64"},
		{name: "relative", gameDir: "steamapps/common"},
		{name: "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := WorkshopDir(tt.gameDir)
			if tt.want == "" {
				if !errors.Is(err, ErrNotInLibrary) {
					t.Errorf("WorkshopDir() = %q, %v; want ErrNotInLibrary", got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("WorkshopDir() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

package main

import (
	"testing"
)

func TestIsGameCommand(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want bool
	}{
		{[]string{"/steam/reaper", "--", "/lib/steamapps/common/Euro Truck Simulator 2/bin/win_x64/eurotrucks2.exe"}, true},
		{[]string{"/lib/steamapps/common/Euro Truck Simulator 2/bin/linux_x64/eurotrucks2"}, true},
		{[]string{"status"}, false},
		{[]string{"set", "game-dir", "/somewhere/bin/win_x64"}, false},
	} {
		if got := isGameCommand(tt.args); got != tt.want {
			t.Errorf("isGameCommand(%q) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

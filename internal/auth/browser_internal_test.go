package auth

import (
	"slices"
	"testing"
)

func TestBrowserCommand(t *testing.T) {
	const url = "https://accounts.spotify.com/authorize?x=1"

	tests := []struct {
		goos     string
		wantName string
		wantArgs []string
	}{
		{goos: "darwin", wantName: "open", wantArgs: []string{url}},
		{goos: "windows", wantName: "rundll32", wantArgs: []string{"url.dll,FileProtocolHandler", url}},
		{goos: "linux", wantName: "xdg-open", wantArgs: []string{url}},
		{goos: "freebsd", wantName: "xdg-open", wantArgs: []string{url}},
	}

	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			name, args := browserCommand(tt.goos, url)
			if name != tt.wantName || !slices.Equal(args, tt.wantArgs) {
				t.Errorf("browserCommand(%q) = %s %v, want %s %v", tt.goos, name, args, tt.wantName, tt.wantArgs)
			}
		})
	}
}

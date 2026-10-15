package spotifyio

import (
	"testing"
)

func TestExtractPlaylistID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantID   string
		wantBool bool
	}{
		{
			name:     "empty input",
			input:    "",
			wantID:   "",
			wantBool: false,
		},
		{
			name:     "plain playlist name with spaces",
			input:    "Public Liked Songs",
			wantID:   "",
			wantBool: false,
		},
		{
			name:     "spotify URI",
			input:    "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M",
			wantID:   "37i9dQZF1DXcBWIGoYBM5M",
			wantBool: true,
		},
		{
			name:     "open.spotify.com URL with params",
			input:    "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M?si=123456789abcdef0",
			wantID:   "37i9dQZF1DXcBWIGoYBM5M",
			wantBool: true,
		},
		{
			name:     "open.spotify.com URL trailing slash",
			input:    "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M/",
			wantID:   "37i9dQZF1DXcBWIGoYBM5M",
			wantBool: true,
		},
		{
			name:     "open.spotify.com URL plain",
			input:    "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M",
			wantID:   "37i9dQZF1DXcBWIGoYBM5M",
			wantBool: true,
		},
		{
			name:     "direct 22 char base62 ID",
			input:    "37i9dQZF1DXcBWIGoYBM5M",
			wantID:   "37i9dQZF1DXcBWIGoYBM5M",
			wantBool: true,
		},
		{
			name:     "short name not 22 chars",
			input:    "Rock Classics",
			wantID:   "",
			wantBool: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotBool := ExtractPlaylistID(tt.input)
			if gotBool != tt.wantBool {
				t.Errorf("ExtractPlaylistID(%q) gotBool = %v, want %v", tt.input, gotBool, tt.wantBool)
			}
			if gotID != tt.wantID {
				t.Errorf("ExtractPlaylistID(%q) gotID = %q, want %q", tt.input, gotID, tt.wantID)
			}
		})
	}
}

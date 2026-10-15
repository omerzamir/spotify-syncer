package sync

import (
	"slices"
	"testing"

	"github.com/zmb3/spotify/v2"

	"spotify-syncer/internal/dedup"
)

func TestCombineSources(t *testing.T) {
	sources := [][]dedup.TrackRef{
		{{ID: "a", ISRC: "A"}, {ID: "b", ISRC: "B"}, {ID: "c"}},
		{{ID: "b", ISRC: "B"}, {ID: "a-album", ISRC: "A"}, {ID: "d", ISRC: "D"}},
		{{ID: "c"}, {ID: "e", ISRC: "E"}, {ID: ""}},
	}

	tests := []struct {
		name        string
		deduplicate bool
		want        []spotify.ID
		wantSkipped int
	}{
		{
			name:        "dedup by ID and ISRC keeps first occurrence order",
			deduplicate: true,
			want:        []spotify.ID{"a", "b", "c", "d", "e"},
			wantSkipped: 3,
		},
		{
			name:        "no dedup keeps everything but empty IDs",
			deduplicate: false,
			want:        []spotify.ID{"a", "b", "c", "b", "a-album", "d", "c", "e"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, skipped := combineSources(sources, tt.deduplicate)
			if !slices.Equal(got, tt.want) {
				t.Errorf("combined = %v, want %v", got, tt.want)
			}
			if skipped != tt.wantSkipped {
				t.Errorf("skipped = %d, want %d", skipped, tt.wantSkipped)
			}
		})
	}
}

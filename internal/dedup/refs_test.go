package dedup_test

import (
	"slices"
	"testing"

	"github.com/omerzamir/spotify/v2"

	"spotify-syncer/internal/dedup"
)

func TestUniqueTracks(t *testing.T) {
	tests := []struct {
		name        string
		input       []dedup.TrackRef
		wantIDs     []spotify.ID
		wantSkipped int
	}{
		{
			name:    "empty",
			input:   nil,
			wantIDs: []spotify.ID{},
		},
		{
			name: "exact ID duplicates keep first occurrence",
			input: []dedup.TrackRef{
				{ID: "a", ISRC: "X"}, {ID: "b", ISRC: "Y"}, {ID: "a", ISRC: "X"},
			},
			wantIDs:     []spotify.ID{"a", "b"},
			wantSkipped: 1,
		},
		{
			name: "same ISRC different IDs is a duplicate",
			input: []dedup.TrackRef{
				{ID: "single", ISRC: "X"}, {ID: "other", ISRC: "Y"}, {ID: "album", ISRC: "X"},
			},
			wantIDs:     []spotify.ID{"single", "other"},
			wantSkipped: 1,
		},
		{
			name: "tracks without ISRC fall back to exact ID",
			input: []dedup.TrackRef{
				{ID: "a"}, {ID: "b"}, {ID: "a"},
			},
			wantIDs:     []spotify.ID{"a", "b"},
			wantSkipped: 1,
		},
		{
			name: "missing ISRC does not match a track that has one",
			input: []dedup.TrackRef{
				{ID: "a", ISRC: "X"}, {ID: "b"},
			},
			wantIDs: []spotify.ID{"a", "b"},
		},
		{
			name: "empty IDs are dropped and not counted",
			input: []dedup.TrackRef{
				{ID: ""}, {ID: "a"}, {ID: "", ISRC: "X"},
			},
			wantIDs: []spotify.ID{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, skipped := dedup.UniqueTracks(tt.input)

			if ids := dedup.TrackIDs(got); !slices.Equal(ids, tt.wantIDs) {
				t.Errorf("ids = %v, want %v", ids, tt.wantIDs)
			}
			if skipped != tt.wantSkipped {
				t.Errorf("skipped = %d, want %d", skipped, tt.wantSkipped)
			}
		})
	}
}

func TestLikedIndexMatches(t *testing.T) {
	index := dedup.NewLikedIndex()
	index.Add("liked-single", "X")
	index.Add("liked-album", "X")
	index.Add("liked-no-isrc", "")
	index.Add("liked-single", "X") // duplicate add is ignored

	tests := []struct {
		name  string
		track dedup.TrackRef
		want  []spotify.ID
	}{
		{name: "exact ID wins", track: dedup.TrackRef{ID: "liked-album", ISRC: "X"}, want: []spotify.ID{"liked-album"}},
		{
			name:  "ISRC matches every liked release",
			track: dedup.TrackRef{ID: "new", ISRC: "X"},
			want:  []spotify.ID{"liked-single", "liked-album"},
		},
		{
			name:  "liked track without ISRC matches by ID",
			track: dedup.TrackRef{ID: "liked-no-isrc"},
			want:  []spotify.ID{"liked-no-isrc"},
		},
		{name: "no ISRC and unknown ID", track: dedup.TrackRef{ID: "new"}, want: nil},
		{name: "unknown ISRC", track: dedup.TrackRef{ID: "new", ISRC: "Z"}, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := index.Matches(tt.track); !slices.Equal(got, tt.want) {
				t.Errorf("Matches(%+v) = %v, want %v", tt.track, got, tt.want)
			}
		})
	}

	if index.Len() != 3 {
		t.Errorf("Len() = %d, want 3", index.Len())
	}
}

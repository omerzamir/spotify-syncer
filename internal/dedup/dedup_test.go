package dedup_test

import (
	"log/slog"
	"maps"
	"slices"
	"testing"

	"github.com/zmb3/spotify/v2"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/dedup"
	"spotify-syncer/internal/spotifyio"
)

func TestDeduplicateTracksSelectsRepresentative(t *testing.T) {
	tests := []struct {
		name  string
		group []spotifyio.LikedTrackData
		want  spotify.ID
	}{
		{
			name: "playable beats newer",
			group: []spotifyio.LikedTrackData{
				{TrackID: "new", ISRC: "X", AddedAt: "2026-02-01T00:00:00Z", IsPlayable: false},
				{TrackID: "old", ISRC: "X", AddedAt: "2026-01-01T00:00:00Z", IsPlayable: true},
			},
			want: "old",
		},
		{
			name: "newest wins among playable",
			group: []spotifyio.LikedTrackData{
				{TrackID: "old", ISRC: "X", AddedAt: "2026-01-01T00:00:00Z", IsPlayable: true},
				{TrackID: "new", ISRC: "X", AddedAt: "2026-02-01T00:00:00Z", IsPlayable: true},
			},
			want: "new",
		},
		{
			name: "equal timestamps tie-break on lowest ID",
			group: []spotifyio.LikedTrackData{
				{TrackID: "zzz", ISRC: "X", AddedAt: "2026-01-01T00:00:00Z", IsPlayable: true},
				{TrackID: "aaa", ISRC: "X", AddedAt: "2026-01-01T00:00:00Z", IsPlayable: true},
			},
			want: "aaa",
		},
		{
			name: "unparseable timestamps tie-break on lowest ID",
			group: []spotifyio.LikedTrackData{
				{TrackID: "zzz", ISRC: "X", AddedAt: "bad", IsPlayable: true},
				{TrackID: "aaa", ISRC: "X", AddedAt: "bad", IsPlayable: true},
			},
			want: "aaa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := dedup.DeduplicateTracks(t.Context(), slog.New(slog.DiscardHandler), newStore(t), tt.group)
			if err != nil {
				t.Fatalf("DeduplicateTracks: %v", err)
			}

			if !slices.Equal(result.RepresentativeTracks, []spotify.ID{tt.want}) {
				t.Errorf("RepresentativeTracks = %v, want [%s]", result.RepresentativeTracks, tt.want)
			}
			if len(result.DuplicateIDs) != len(tt.group)-1 {
				t.Errorf("DuplicateIDs = %v, want %d", result.DuplicateIDs, len(tt.group)-1)
			}
		})
	}
}

func TestDeduplicateTracksKeepsTracksWithoutISRCAndCachesISRCs(t *testing.T) {
	store := newStore(t)
	tracks := []spotifyio.LikedTrackData{
		{TrackID: "a", ISRC: "X", IsPlayable: true},
		{TrackID: "b", IsPlayable: true},
		{TrackID: "c", IsPlayable: true},
	}

	result, err := dedup.DeduplicateTracks(t.Context(), slog.New(slog.DiscardHandler), store, tracks)
	if err != nil {
		t.Fatalf("DeduplicateTracks: %v", err)
	}

	if got := slices.Sorted(
		slices.Values(result.RepresentativeTracks),
	); !slices.Equal(
		got,
		[]spotify.ID{"a", "b", "c"},
	) {
		t.Errorf("RepresentativeTracks = %v, want a, b, c", got)
	}

	cached, err := store.LoadTrackISRC()
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"a": "X", "b": "", "c": ""}; !maps.Equal(cached, want) {
		t.Errorf("ISRC cache = %v, want %v", cached, want)
	}
}

func newStore(t *testing.T) *cache.Store {
	t.Helper()

	store, err := cache.NewStore(t.TempDir(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

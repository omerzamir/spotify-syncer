package classifier_test

import (
	"log/slog"
	"maps"
	"slices"
	"testing"

	"github.com/zmb3/spotify/v2"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/classifier"
	"spotify-syncer/internal/config"
)

func TestClassify(t *testing.T) {
	cfg := config.GenreConfig{MaxBuckets: 5, MinBucketSize: 2, GenresPerArtist: 2}

	trackIDs := []spotify.ID{"t0", "t1", "t2", "t3", "t4", "t5", "t6"}
	trackArtists := map[string][]string{
		"t0": {"rock-artist"},
		"t1": {"rock-artist"},
		"t2": {"pop-artist"},
		"t3": {"rock-artist"},
		"t4": {"pop-artist"},
		"t5": {"both-artist"},
		"t6": {"niche-artist"},
	}
	artistGenres := map[string]cache.ArtistGenreEntry{
		"rock-artist":  {Genres: []string{"rock", "indie", "ignored-third"}},
		"pop-artist":   {Genres: []string{"pop"}},
		"both-artist":  {Genres: []string{"rock", "pop"}},
		"niche-artist": {Genres: []string{"niche"}},
	}

	got, err := classifier.Classify(
		t.Context(),
		slog.New(slog.DiscardHandler),
		cfg,
		trackIDs,
		trackArtists,
		artistGenres,
	)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}

	// rock has 4 tracks and wins first; pop keeps t2, t4; indie loses all its
	// tracks to rock; niche is below the minimum size; ignored-third is past
	// GenresPerArtist.
	want := map[string][]spotify.ID{
		"rock": {"t0", "t1", "t3", "t5"},
		"pop":  {"t2", "t4"},
	}

	if !maps.EqualFunc(got.Buckets, want, slices.Equal) {
		t.Errorf("Buckets = %v, want %v", got.Buckets, want)
	}
}

func TestClassifyBreaksTiesAlphabetically(t *testing.T) {
	cfg := config.GenreConfig{MaxBuckets: 1, MinBucketSize: 1, GenresPerArtist: 3}

	trackIDs := []spotify.ID{"t1", "t2"}
	trackArtists := map[string][]string{"t1": {"a"}, "t2": {"a"}}
	artistGenres := map[string]cache.ArtistGenreEntry{"a": {Genres: []string{"zeta", "alpha", "mid"}}}

	for range 20 {
		got, err := classifier.Classify(
			t.Context(),
			slog.New(slog.DiscardHandler),
			cfg,
			trackIDs,
			trackArtists,
			artistGenres,
		)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		if _, ok := got.Buckets["alpha"]; !ok || len(got.Buckets) != 1 {
			t.Fatalf("Buckets = %v, want only alpha", got.Buckets)
		}
	}
}

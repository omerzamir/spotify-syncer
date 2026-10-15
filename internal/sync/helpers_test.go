package sync_test

import (
	"log/slog"
	"testing"
	"time"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func testConfig() config.Config {
	return config.Config{
		Sync: config.SyncConfig{
			MirrorPlaylistName: "Mirror",
			BatchSize:          100,
		},
		Genre: config.GenreConfig{
			MaxBuckets:           5,
			MinBucketSize:        2,
			GenresPerArtist:      3,
			BucketPlaylistPrefix: "Liked -- ",
			ArtistGenreTTL:       24 * time.Hour,
		},
	}
}

func testStore(t *testing.T) *cache.Store {
	t.Helper()

	store, err := cache.NewStore(t.TempDir(), testLogger())
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	return store
}

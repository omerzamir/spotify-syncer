package sync_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/config"
	"spotify-syncer/internal/spotifyio/spotifytest"
	"spotify-syncer/internal/sync"
)

// genreTestConfig returns a config whose genre buckets have no minimum size.
func genreTestConfig() config.Config {
	cfg := testConfig()
	cfg.Genre.MinBucketSize = 1
	return cfg
}

// newGenreFake returns a fake with two liked rock tracks (a, b) and the genres
// of the artists used by the genre tests.
func newGenreFake(t *testing.T) *spotifytest.Server {
	t.Helper()

	fake := spotifytest.NewServer(t)
	fake.SetArtistGenres("rocker", "rock")
	fake.SetArtistGenres("newcomer", "jazz")
	fake.Like(likedAt(1), spotifytest.Track{ID: "a", ISRC: "A", ArtistIDs: []string{"rocker"}})
	fake.Like(likedAt(2), spotifytest.Track{ID: "b", ISRC: "B", ArtistIDs: []string{"rocker"}})

	return fake
}

// likeNewcomer likes a jazz track by an artist the syncer has not seen yet.
func likeNewcomer(fake *spotifytest.Server) {
	fake.Like(likedAt(10), spotifytest.Track{ID: "c", ISRC: "C", ArtistIDs: []string{"newcomer"}})
}

func requireGenresPending(t *testing.T, store *cache.Store, want bool) {
	t.Helper()

	checkpoint, err := store.LoadCheckpoint()
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if checkpoint.GenresPending != want {
		t.Errorf("GenresPending = %t, want %t", checkpoint.GenresPending, want)
	}
}

func TestRunRetriesGenreSyncThatFailed(t *testing.T) {
	fake := newGenreFake(t)
	store := testStore(t)
	cfg := genreTestConfig()
	enabled := sync.RunOptions{Mode: sync.ModeFast, EnableGenres: true}

	runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFull, EnableGenres: true})

	// The mirror gets the new track, then the genre lookup for its artist fails.
	likeNewcomer(fake)
	fake.SetStatus("GET /artists", http.StatusInternalServerError)

	if _, err := sync.Run(t.Context(), testLogger(), cfg, fake.Client(), store,
		sync.RunOptions{Mode: sync.ModeFull, EnableGenres: true}); err == nil {
		t.Fatal("Run() error = nil, want failure from the artist lookup")
	}
	requireGenresPending(t, store, true)

	// Nothing new is liked, so the mirror does not change; the retry must
	// still sync the genre playlists.
	fake.SetStatus("GET /artists", 0)
	result := runSync(t, fake, cfg, store, enabled)

	if result.Metrics.Mode != "fast" || result.Metrics.MirrorAdded != 0 || result.Metrics.BucketsCreated != 2 {
		t.Errorf("metrics = %+v, want a fast run with no mirror change and 2 buckets", result.Metrics)
	}
	if got := mirrorTrackIDs(t, fake, "Liked -- jazz"); !slices.Equal(got, []string{"c"}) {
		t.Errorf("jazz bucket = %v, want [c]", got)
	}
	requireGenresPending(t, store, false)
}

func TestRunSyncsGenresSkippedWhileDisabled(t *testing.T) {
	fake := newGenreFake(t)
	store := testStore(t)
	cfg := genreTestConfig()

	runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFull, EnableGenres: true})

	likeNewcomer(fake)
	runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFast})

	if _, found := fake.PlaylistByName("Liked -- jazz"); found {
		t.Fatal("jazz bucket exists although genres were disabled")
	}
	requireGenresPending(t, store, true)

	result := runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFast, EnableGenres: true})

	if result.Metrics.MirrorAdded != 0 || result.Metrics.BucketsCreated != 2 {
		t.Errorf("metrics = %+v, want no mirror change and 2 buckets", result.Metrics)
	}
	if got := mirrorTrackIDs(t, fake, "Liked -- jazz"); !slices.Equal(got, []string{"c"}) {
		t.Errorf("jazz bucket = %v, want [c]", got)
	}
	requireGenresPending(t, store, false)
}

func TestRunSkipsGenresInSteadyState(t *testing.T) {
	fake := newGenreFake(t)
	store := testStore(t)
	cfg := genreTestConfig()

	runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFull, EnableGenres: true})
	requireGenresPending(t, store, false)

	// Even with every cached artist expired, an unchanged mirror must not
	// trigger any genre lookups.
	cfg.Genre.ArtistGenreTTL = time.Nanosecond
	fake.ResetRequests()

	runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFast, EnableGenres: true})

	if n := fake.CountRequests("GET /artists"); n != 0 {
		t.Errorf("artist lookups = %d, want 0 when nothing changed and nothing is pending", n)
	}
	requireGenresPending(t, store, false)
}

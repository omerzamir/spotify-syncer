package sync_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/config"
	"spotify-syncer/internal/spotifyio/spotifytest"
	"spotify-syncer/internal/sync"
)

// likedAt returns a fixed base time plus n minutes.
func likedAt(n int) time.Time {
	return time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute)
}

func runSync(
	t *testing.T,
	fake *spotifytest.Server,
	cfg config.Config,
	store *cache.Store,
	opts sync.RunOptions,
) sync.RunResult {
	t.Helper()

	result, err := sync.Run(t.Context(), testLogger(), cfg, fake.Client(), store, opts)
	if err != nil {
		t.Fatalf("Run(%s): %v", opts.Mode, err)
	}
	return result
}

func mirrorTrackIDs(t *testing.T, fake *spotifytest.Server, name string) []string {
	t.Helper()

	mirror, ok := fake.PlaylistByName(name)
	if !ok {
		t.Fatalf("playlist %q not found", name)
	}
	return fake.PlaylistTrackIDs(mirror.ID)
}

func TestRunFastWithoutStateFallsBackToFull(t *testing.T) {
	fake := spotifytest.NewServer(t)
	store := testStore(t)
	cfg := testConfig()

	fake.Like(likedAt(1), spotifytest.Track{ID: "old", ISRC: "A"})
	fake.Like(likedAt(2), spotifytest.Track{ID: "new", ISRC: "B"})

	result := runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFast})

	if result.Metrics.Mode != "full" {
		t.Errorf("Mode = %q, want full (fallback)", result.Metrics.Mode)
	}
	if got, want := mirrorTrackIDs(
		t,
		fake,
		cfg.Sync.MirrorPlaylistName,
	), []string{
		"old",
		"new",
	}; !slices.Equal(
		got,
		want,
	) {
		t.Errorf("mirror = %v, want %v (oldest liked first)", got, want)
	}

	checkpoint, err := store.LoadCheckpoint()
	if err != nil || !checkpoint.LatestAddedAt.Equal(likedAt(2)) {
		t.Errorf("checkpoint = %v, %v; want %v", checkpoint.LatestAddedAt, err, likedAt(2))
	}
}

func TestRunFastAppendsOnlyNewTracksAndFullCleansUp(t *testing.T) {
	fake := spotifytest.NewServer(t)
	store := testStore(t)
	cfg := testConfig()

	// 120 liked tracks: more than two pages of liked songs.
	for i := range 120 {
		fake.Like(likedAt(i), spotifytest.Track{ID: fmt.Sprintf("t%03d", i), ISRC: fmt.Sprintf("I%03d", i)})
	}
	runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFull})

	fake.Unlike("t005")
	fake.Like(likedAt(200), spotifytest.Track{ID: "fresh", ISRC: "FRESH"})
	fake.Like(likedAt(201), spotifytest.Track{ID: "t000-remaster", ISRC: "I000"}) // same recording as t000
	fake.ResetRequests()

	fast := runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFast})

	m := fast.Metrics
	if m.Mode != "fast" || m.NewTracks != 2 || m.MirrorAdded != 1 || m.MirrorRemoved != 0 || m.DedupRemoved != 1 {
		t.Errorf("fast metrics = %+v, want fast, 2 new, 1 added, 0 removed, 1 dedup", m)
	}
	if n := fake.CountRequests("GET /me/tracks"); n != 1 {
		t.Errorf("fast run fetched %d liked pages, want 1", n)
	}

	mirror := mirrorTrackIDs(t, fake, cfg.Sync.MirrorPlaylistName)
	if !slices.Contains(mirror, "t005") {
		t.Error("fast run removed an unliked track; only full mode should")
	}
	if mirror[len(mirror)-1] != "fresh" || slices.Contains(mirror, "t000-remaster") {
		t.Errorf("mirror tail = %v, want fresh appended and the remaster skipped", mirror[len(mirror)-3:])
	}

	checkpoint, err := store.LoadCheckpoint()
	if err != nil || !checkpoint.LatestAddedAt.Equal(likedAt(201)) {
		t.Errorf("checkpoint = %v, %v; want %v", checkpoint.LatestAddedAt, err, likedAt(201))
	}

	// Full mode removes the unliked track and, unlike fast mode, swaps t000
	// for its newer release of the same recording.
	full := runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFull})

	if full.Metrics.MirrorRemoved != 2 || full.Metrics.MirrorAdded != 1 {
		t.Errorf("full run removed %d and added %d, want 2 (t005, t000) and 1 (t000-remaster)",
			full.Metrics.MirrorRemoved, full.Metrics.MirrorAdded)
	}

	mirror = mirrorTrackIDs(t, fake, cfg.Sync.MirrorPlaylistName)
	if slices.Contains(mirror, "t005") || slices.Contains(mirror, "t000") || !slices.Contains(mirror, "t000-remaster") {
		t.Errorf("mirror after full run = %v, want t005 and t000 gone and t000-remaster present", mirror)
	}
}

func TestRunFastDoesNotAdvanceCheckpointOnFailure(t *testing.T) {
	fake := spotifytest.NewServer(t)
	store := testStore(t)
	cfg := testConfig()

	fake.Like(likedAt(1), spotifytest.Track{ID: "a", ISRC: "A"})
	runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFull})

	mirror, _ := fake.PlaylistByName(cfg.Sync.MirrorPlaylistName)
	fake.SetStatus("POST /playlists/"+mirror.ID+"/items", http.StatusInternalServerError)
	fake.Like(likedAt(2), spotifytest.Track{ID: "b", ISRC: "B"})

	if _, err := sync.Run(t.Context(), testLogger(), cfg, fake.Client(), store,
		sync.RunOptions{Mode: sync.ModeFast}); err == nil {
		t.Fatal("Run() error = nil, want failure from playlist add")
	}

	checkpoint, err := store.LoadCheckpoint()
	if err != nil || !checkpoint.LatestAddedAt.Equal(likedAt(1)) {
		t.Errorf("checkpoint = %v, %v; want unchanged %v", checkpoint.LatestAddedAt, err, likedAt(1))
	}

	fake.SetStatus("POST /playlists/"+mirror.ID+"/items", 0)
	retry := runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFast})

	if retry.Metrics.MirrorAdded != 1 {
		t.Errorf("retry added %d tracks, want 1", retry.Metrics.MirrorAdded)
	}
}

func TestRunFastOnlyFetchesGenresForNewArtists(t *testing.T) {
	fake := spotifytest.NewServer(t)
	store := testStore(t)
	cfg := testConfig()
	cfg.Genre.MinBucketSize = 1
	cfg.Genre.ArtistGenreTTL = time.Nanosecond // every cached entry is expired

	fake.SetArtistGenres("known", "rock")
	fake.SetArtistGenres("newcomer", "jazz")
	fake.Like(likedAt(1), spotifytest.Track{ID: "a", ISRC: "A", ArtistIDs: []string{"known"}})
	runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFull, EnableGenres: true})

	fake.Like(likedAt(2),
		spotifytest.Track{ID: "b", ISRC: "B", ArtistIDs: []string{"known"}},
		spotifytest.Track{ID: "c", ISRC: "C", ArtistIDs: []string{"newcomer"}},
	)
	fake.ResetRequests()

	result := runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFast, EnableGenres: true})

	if n := fake.CountRequests("GET /artists"); n != 1 {
		t.Errorf("artist lookups = %d, want 1 (newcomer only; expired entries wait for full mode)", n)
	}
	if result.Metrics.BucketsCreated != 2 {
		t.Errorf("BucketsCreated = %d, want 2 (rock, jazz)", result.Metrics.BucketsCreated)
	}
	if got := mirrorTrackIDs(t, fake, "Liked -- jazz"); !slices.Equal(got, []string{"c"}) {
		t.Errorf("jazz bucket = %v, want [c]", got)
	}
}

package sync_test

import (
	"slices"
	"testing"
	"time"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/spotifyio/spotifytest"
	"spotify-syncer/internal/sync"
)

// checkpointTime is the time of the last sync run in reverse tests.
func checkpointTime() time.Time {
	return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
}

func TestReverseWithoutSyncStateFetchesLibraryAndMatchesByISRC(t *testing.T) {
	fake := spotifytest.NewServer(t)
	fake.Like(checkpointTime(),
		spotifytest.Track{ID: "liked-album", ISRC: "Y"},
		spotifytest.Track{ID: "liked-no-isrc"},
	)
	playlistID := fake.AddPlaylist(spotifytest.Playlist{
		ID:   "37i9dQZF1DXcBWIGoYBM5M",
		Name: "Source",
		Tracks: []spotifytest.Track{
			{ID: "a-single", ISRC: "X"},
			{ID: "b-single", ISRC: "Y"}, // same recording as liked-album
			{ID: "a-album", ISRC: "X"},  // duplicate of a-single in the playlist
			{ID: "c"},
			{ID: "liked-no-isrc"}, // no ISRC: matched by exact ID
		},
	})

	result, err := sync.Reverse(t.Context(), testLogger(), testConfig(), fake.Client(), testStore(t),
		sync.ReverseConfig{PlaylistIdentifier: playlistID, BatchSize: 50})
	if err != nil {
		t.Fatalf("Reverse: %v", err)
	}

	if result.TotalTracks != 4 || result.DuplicatesSkipped != 1 || result.AlreadyLiked != 2 || result.NewlyAdded != 2 {
		t.Errorf("result = %+v, want total 4, duplicates 1, already liked 2, added 2", result)
	}

	got := fake.LikedIDs()
	slices.Sort(got)
	want := []string{"a-single", "c", "liked-album", "liked-no-isrc"}
	if !slices.Equal(got, want) {
		t.Errorf("liked = %v, want %v", got, want)
	}

	if n := fake.CountRequests("GET /me/tracks/contains"); n != 0 {
		t.Errorf("library contains called %d times, want 0 (full fetch is authoritative)", n)
	}
}

func TestReverseWithSyncStateUsesCacheAndVerifiesMatches(t *testing.T) {
	fake := spotifytest.NewServer(t)
	store := testStore(t)

	// State from the last sync at the checkpoint: still-liked was liked, unliked-since was
	// liked but has been removed from the library since.
	fake.Like(checkpointTime(), spotifytest.Track{ID: "still-liked", ISRC: "X"})
	if err := store.SaveCheckpoint(cache.Checkpoint{LatestAddedAt: checkpointTime()}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTrackISRC(map[string]string{"still-liked": "X", "unliked-since": "Y"}); err != nil {
		t.Fatal(err)
	}

	// Liked after the checkpoint, so only visible through the live delta.
	fake.Like(checkpointTime().Add(time.Hour), spotifytest.Track{ID: "liked-after", ISRC: "Z"})

	fake.AddPlaylist(spotifytest.Playlist{
		Name: "Source",
		Tracks: []spotifytest.Track{
			{ID: "p-x", ISRC: "X"}, // matches cached still-liked: verified, skip
			{ID: "p-y", ISRC: "Y"}, // matches cached unliked-since: verified false, add
			{ID: "p-z", ISRC: "Z"}, // matches fresh liked-after: skip, no verification
			{ID: "p-w", ISRC: "W"}, // no match: add
		},
	})
	fake.ResetRequests()

	result, err := sync.Reverse(t.Context(), testLogger(), testConfig(), fake.Client(), store,
		sync.ReverseConfig{PlaylistIdentifier: "Source", BatchSize: 50})
	if err != nil {
		t.Fatalf("Reverse: %v", err)
	}

	if result.AlreadyLiked != 2 || result.NewlyAdded != 2 {
		t.Errorf("result = %+v, want already liked 2, added 2", result)
	}

	got := fake.LikedIDs()
	slices.Sort(got)
	want := []string{"liked-after", "p-w", "p-y", "still-liked"}
	if !slices.Equal(got, want) {
		t.Errorf("liked = %v, want %v", got, want)
	}

	if n := fake.CountRequests("GET /me/tracks/contains"); n != 1 {
		t.Errorf("library contains called %d times, want 1", n)
	}
	if n := fake.CountRequests("GET /me/tracks"); n != 1 {
		t.Errorf("liked tracks fetched %d times, want 1 page (delta since checkpoint)", n)
	}
}

func TestReverseDryRunSavesNothing(t *testing.T) {
	fake := spotifytest.NewServer(t)
	fake.AddPlaylist(spotifytest.Playlist{
		Name:   "Source",
		Tracks: []spotifytest.Track{{ID: "a", ISRC: "X"}},
	})

	result, err := sync.Reverse(t.Context(), testLogger(), testConfig(), fake.Client(), testStore(t),
		sync.ReverseConfig{PlaylistIdentifier: "Source", BatchSize: 50, DryRun: true})
	if err != nil {
		t.Fatalf("Reverse: %v", err)
	}

	if result.NewlyAdded != 1 || !result.DryRun {
		t.Errorf("result = %+v, want 1 would-be addition in dry run", result)
	}
	if liked := fake.LikedIDs(); len(liked) != 0 {
		t.Errorf("liked = %v, want none", liked)
	}
	if n := fake.CountRequests("PUT /me/tracks"); n != 0 {
		t.Errorf("save called %d times in dry run", n)
	}
}

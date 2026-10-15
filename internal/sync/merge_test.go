package sync_test

import (
	"slices"
	"testing"

	"spotify-syncer/internal/spotifyio/spotifytest"
	"spotify-syncer/internal/sync"
)

func TestMergePlaylistsDeduplicatesByISRC(t *testing.T) {
	fake := spotifytest.NewServer(t)
	fake.AddPlaylist(spotifytest.Playlist{Name: "One", Tracks: []spotifytest.Track{
		{ID: "a-single", ISRC: "A"}, {ID: "b", ISRC: "B"},
	}})
	fake.AddPlaylist(spotifytest.Playlist{Name: "Two", Tracks: []spotifytest.Track{
		{ID: "a-album", ISRC: "A"}, {ID: "c", ISRC: "C"},
	}})
	fake.AddPlaylist(spotifytest.Playlist{Name: "Three", Tracks: []spotifytest.Track{
		{ID: "b", ISRC: "B"}, {ID: "d"},
	}})

	result, err := sync.MergePlaylists(t.Context(), testLogger(), testConfig(), fake.Client(), sync.MergeConfig{
		Sources:     []string{"One", "Two", "Three"},
		TargetName:  "Merged",
		Public:      true,
		Deduplicate: true,
	})
	if err != nil {
		t.Fatalf("MergePlaylists: %v", err)
	}

	if result.TotalTracks != 6 || result.UniqueTracks != 4 || result.DuplicatesSkipped != 2 {
		t.Errorf("result = %+v, want total 6, unique 4, skipped 2", result)
	}

	merged, ok := fake.PlaylistByName("Merged")
	if !ok {
		t.Fatal("merged playlist was not created")
	}
	if merged.ID != result.TargetID {
		t.Errorf("target id = %q, created playlist id = %q", result.TargetID, merged.ID)
	}

	want := []string{"a-single", "b", "c", "d"}
	if got := fake.PlaylistTrackIDs(merged.ID); !slices.Equal(got, want) {
		t.Errorf("merged tracks = %v, want %v", got, want)
	}
}

func TestMergePlaylistsDryRunCreatesNothing(t *testing.T) {
	fake := spotifytest.NewServer(t)
	fake.AddPlaylist(spotifytest.Playlist{Name: "One", Tracks: []spotifytest.Track{{ID: "a"}}})
	fake.AddPlaylist(spotifytest.Playlist{Name: "Two", Tracks: []spotifytest.Track{{ID: "a"}}})

	result, err := sync.MergePlaylists(t.Context(), testLogger(), testConfig(), fake.Client(), sync.MergeConfig{
		Sources:     []string{"One", "Two"},
		TargetName:  "Merged",
		Deduplicate: true,
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("MergePlaylists: %v", err)
	}

	if result.UniqueTracks != 1 || result.TargetID != "" {
		t.Errorf("result = %+v, want 1 unique track and no target", result)
	}
	if _, ok := fake.PlaylistByName("Merged"); ok {
		t.Error("dry run created the merged playlist")
	}
}

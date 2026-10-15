package sync_test

import (
	"slices"
	"testing"

	"spotify-syncer/internal/spotifyio/spotifytest"
	"spotify-syncer/internal/sync"
)

// likeRockTracks likes the given track IDs, oldest first, all by one rock artist.
func likeRockTracks(fake *spotifytest.Server, firstMinute int, ids ...string) {
	for i, id := range ids {
		fake.Like(likedAt(firstMinute+i), spotifytest.Track{ID: id, ISRC: "ISRC-" + id, ArtistIDs: []string{"rocker"}})
	}
}

func TestRunRecreatesDeletedMirrorWithAllTracks(t *testing.T) {
	tests := []struct {
		name string
		mode sync.Mode
	}{
		{name: "full", mode: sync.ModeFull},
		{name: "fast", mode: sync.ModeFast},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := spotifytest.NewServer(t)
			store := testStore(t)
			cfg := testConfig()

			likeRockTracks(fake, 1, "a", "b", "c")
			runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFull})

			oldMirror, ok := fake.PlaylistByName(cfg.Sync.MirrorPlaylistName)
			if !ok {
				t.Fatal("first run did not create the mirror playlist")
			}
			fake.DeletePlaylist(oldMirror.ID)

			want := []string{"a", "b", "c"}
			if tt.mode == sync.ModeFast {
				likeRockTracks(fake, 10, "d")
				want = append(want, "d")
			}

			runSync(t, fake, cfg, store, sync.RunOptions{Mode: tt.mode})

			newMirror, ok := fake.PlaylistByName(cfg.Sync.MirrorPlaylistName)
			if !ok || newMirror.ID == oldMirror.ID {
				t.Fatalf("mirror = %+v (found %t), want a newly created playlist", newMirror, ok)
			}
			if got := fake.PlaylistTrackIDs(newMirror.ID); !slices.Equal(got, want) {
				t.Errorf("new mirror = %v, want %v", got, want)
			}
		})
	}
}

func TestRunRecreatesDeletedBucketWithAllTracks(t *testing.T) {
	fake := spotifytest.NewServer(t)
	store := testStore(t)
	cfg := testConfig()
	cfg.Genre.MinBucketSize = 1

	fake.SetArtistGenres("rocker", "rock")
	likeRockTracks(fake, 1, "a", "b", "c")
	runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFull, EnableGenres: true})

	const bucketName = "Liked -- rock"

	oldMirror, _ := fake.PlaylistByName(cfg.Sync.MirrorPlaylistName)
	oldBucket, ok := fake.PlaylistByName(bucketName)
	if !ok {
		t.Fatal("first run did not create the rock bucket")
	}
	fake.DeletePlaylist(oldMirror.ID) // changes the mirror, so genre sync runs again
	fake.DeletePlaylist(oldBucket.ID)

	runSync(t, fake, cfg, store, sync.RunOptions{Mode: sync.ModeFull, EnableGenres: true})

	newBucket, ok := fake.PlaylistByName(bucketName)
	if !ok || newBucket.ID == oldBucket.ID {
		t.Fatalf("bucket = %+v (found %t), want a newly created playlist", newBucket, ok)
	}
	if got, want := fake.PlaylistTrackIDs(newBucket.ID), []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("new bucket = %v, want %v", got, want)
	}
}

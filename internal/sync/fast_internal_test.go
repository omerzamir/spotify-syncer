package sync

import (
	"slices"
	"testing"

	"github.com/zmb3/spotify/v2"

	"spotify-syncer/internal/spotifyio"
)

func TestAppendNewTracks(t *testing.T) {
	mirror := []spotify.ID{"m1", "m2", "m-no-isrc"}
	known := map[string]string{"m1": "A", "m2": "B", "m-no-isrc": ""}

	// Newest first, as fetched from Liked Songs.
	newTracks := []spotifyio.LikedTrackData{
		{TrackID: "n-dup-of-n1", ISRC: "C"},
		{TrackID: "m2"},                     // already mirrored (re-liked)
		{TrackID: "n-no-isrc"},              // no ISRC: kept by ID
		{TrackID: "n-dup-of-m1", ISRC: "A"}, // same recording as m1
		{TrackID: "n1", ISRC: "C"},
	}

	added, duplicates := appendNewTracks(mirror, known, newTracks)

	if want := []spotify.ID{"n1", "n-no-isrc"}; !slices.Equal(added, want) {
		t.Errorf("added = %v, want %v (oldest first)", added, want)
	}
	if want := []spotify.ID{"n-dup-of-m1", "n-dup-of-n1"}; !slices.Equal(duplicates, want) {
		t.Errorf("duplicates = %v, want %v", duplicates, want)
	}
}

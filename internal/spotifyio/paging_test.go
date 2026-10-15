package spotifyio_test

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/zmb3/spotify/v2"

	"spotify-syncer/internal/spotifyio"
	"spotify-syncer/internal/spotifyio/spotifytest"
)

// likeSequence likes n tracks one minute apart starting at base, so track i
// (0-based, oldest first) has AddedAt base+i minutes.
func likeSequence(fake *spotifytest.Server, base time.Time, n int) {
	for i := range n {
		fake.Like(base.Add(time.Duration(i)*time.Minute), spotifytest.Track{
			ID:   fmt.Sprintf("t%03d", i),
			ISRC: fmt.Sprintf("ISRC%03d", i),
		})
	}
}

func TestFetchLikedTracksSinceStopsAtCheckpoint(t *testing.T) {
	fake := spotifytest.NewServer(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	likeSequence(fake, base, 120) // 3 pages of 50, newest first

	// Tracks t110..t119 are after the checkpoint; t110 is exactly at it.
	since := base.Add(110 * time.Minute)
	got, err := spotifyio.FetchLikedTracksSince(
		context.Background(),
		slog.New(slog.DiscardHandler),
		fake.Client(),
		since,
	)
	if err != nil {
		t.Fatalf("spotifyio.FetchLikedTracksSince: %v", err)
	}

	if len(got) != 10 {
		t.Fatalf("got %d tracks, want 10", len(got))
	}
	if got[0].TrackID != "t119" || got[9].TrackID != "t110" {
		t.Errorf("got %s..%s, want t119..t110", got[0].TrackID, got[9].TrackID)
	}
	if got[0].ISRC != "ISRC119" || got[0].AddedAt == "" {
		t.Errorf("first track = %+v, want ISRC and AddedAt populated", got[0])
	}
	if n := fake.CountRequests("GET /me/tracks"); n != 1 {
		t.Errorf("fetched %d pages, want 1", n)
	}
}

func TestFetchLikedTracksSinceZeroFetchesAll(t *testing.T) {
	fake := spotifytest.NewServer(t)
	likeSequence(fake, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 120)

	got, err := spotifyio.FetchLikedTracksSince(
		context.Background(),
		slog.New(slog.DiscardHandler),
		fake.Client(),
		time.Time{},
	)
	if err != nil {
		t.Fatalf("spotifyio.FetchLikedTracksSince: %v", err)
	}

	if len(got) != 120 {
		t.Errorf("got %d tracks, want 120", len(got))
	}
	if n := fake.CountRequests("GET /me/tracks"); n != 3 {
		t.Errorf("fetched %d pages, want 3", n)
	}
}

func TestFetchPlaylistTracksSkipsLocalFilesAndKeepsISRC(t *testing.T) {
	fake := spotifytest.NewServer(t)
	id := fake.AddPlaylist(spotifytest.Playlist{Name: "P", Tracks: []spotifytest.Track{
		{ID: "a", ISRC: "X"},
		{ID: ""}, // local file
		{ID: "b"},
	}})

	got, err := spotifyio.FetchPlaylistTracks(
		context.Background(),
		slog.New(slog.DiscardHandler),
		fake.Client(),
		spotify.ID(id),
	)
	if err != nil {
		t.Fatalf("spotifyio.FetchPlaylistTracks: %v", err)
	}

	want := []spotifyio.PlaylistTrackData{{TrackID: "a", ISRC: "X"}, {TrackID: "b"}}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

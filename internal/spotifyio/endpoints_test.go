package spotifyio_test

import (
	"log/slog"
	"net/http"
	"slices"
	"testing"

	"github.com/omerzamir/spotify/v2"

	"spotify-syncer/internal/spotifyio"
	"spotify-syncer/internal/spotifyio/spotifytest"
)

// exercise runs every migrated operation once against fake and returns the
// playlist it used.
func exercise(t *testing.T, fake *spotifytest.Server) (string, error) {
	t.Helper()

	ctx, logger, client := t.Context(), slog.New(slog.DiscardHandler), fake.Client()
	fake.AddCatalogTracks(spotifytest.Track{ID: "a", ISRC: "A"}, spotifytest.Track{ID: "b", ISRC: "B"})

	id, err := spotifyio.CreatePlaylist(ctx, logger, client, "P", "", true)
	if err != nil {
		return "", err
	}
	if _, err = spotifyio.BatchAddTracks(ctx, logger, client, id, []spotify.ID{"a", "b"}, 100); err != nil {
		return "", err
	}
	if _, err = spotifyio.BatchRemoveTracks(ctx, logger, client, id, []spotify.ID{"b"}, 100); err != nil {
		return "", err
	}
	if _, err = spotifyio.FetchPlaylistTracks(ctx, logger, client, id); err != nil {
		return "", err
	}
	if err = spotifyio.BatchAddTracksToLibrary(ctx, logger, client, []spotify.ID{"a"}, 40); err != nil {
		return "", err
	}
	if _, err = spotifyio.BatchCheckTracksInLibrary(ctx, logger, client, []spotify.ID{"a", "b"}); err != nil {
		return "", err
	}

	return string(id), nil
}

func TestOperationsUseFebruary2026Endpoints(t *testing.T) {
	fake := spotifytest.NewServer(t)

	id, err := exercise(t, fake)
	if err != nil {
		t.Fatalf("exercise: %v", err)
	}

	want := []string{
		"POST /me/playlists",
		"POST /playlists/" + id + "/items",
		"DELETE /playlists/" + id + "/items",
		"GET /playlists/" + id + "/items",
		"PUT /me/library",
		"GET /me/library/contains",
	}
	if got := fake.Requests(); !slices.Equal(got, want) {
		t.Errorf("requests =\n  %v\nwant\n  %v", got, want)
	}
	if got := fake.PlaylistTrackIDs(id); !slices.Equal(got, []string{"a"}) {
		t.Errorf("playlist = %v, want [a]", got)
	}
	if got := fake.LikedIDs(); !slices.Equal(got, []string{"a"}) {
		t.Errorf("liked = %v, want [a]", got)
	}
}

func TestOperationsFallBackToLegacyEndpointsOnClientErrors(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusGone} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			fake := spotifytest.NewServer(t)
			fake.SetStatus("POST /me/playlists", status)
			fake.SetStatus("PUT /me/library", status)
			fake.SetStatus("GET /me/library/contains", status)
			// The playlist ID is deterministic in the fake: pl1.
			fake.SetStatus("POST /playlists/pl1/items", status)
			fake.SetStatus("DELETE /playlists/pl1/items", status)
			fake.SetStatus("GET /playlists/pl1/items", status)

			id, err := exercise(t, fake)
			if err != nil {
				t.Fatalf("exercise: %v", err)
			}

			requests := fake.Requests()
			for _, legacy := range []string{
				"POST /users/fake-user/playlists",
				"POST /playlists/" + id + "/tracks",
				"DELETE /playlists/" + id + "/tracks",
				"GET /playlists/" + id + "/tracks",
				"PUT /me/tracks",
				"GET /me/tracks/contains",
			} {
				if !slices.Contains(requests, legacy) {
					t.Errorf("legacy request %q missing from %v", legacy, requests)
				}
			}
			if got := fake.PlaylistTrackIDs(id); !slices.Equal(got, []string{"a"}) {
				t.Errorf("playlist = %v, want [a]", got)
			}
		})
	}
}

func TestOperationsDoNotFallBackOnAuthOrServerErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			fake := spotifytest.NewServer(t)
			fake.SetStatus("POST /me/playlists", status)

			_, err := spotifyio.CreatePlaylist(t.Context(), slog.New(slog.DiscardHandler), fake.Client(), "P", "", true)
			if err == nil {
				t.Fatal("CreatePlaylist() error = nil, want the current endpoint's error")
			}
			if n := fake.CountRequests("POST /users/fake-user/playlists"); n != 0 {
				t.Errorf("legacy endpoint called %d times, want 0", n)
			}
		})
	}
}

func TestUnfollowPlaylistUsesLibraryEndpointWithFallback(t *testing.T) {
	for _, tt := range []struct {
		name        string
		failCurrent bool
		wantRequest string
	}{
		{name: "current", wantRequest: "DELETE /me/library"},
		{name: "legacy fallback", failCurrent: true, wantRequest: "DELETE /playlists/pl1/followers"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := spotifytest.NewServer(t)
			id := fake.AddPlaylist(spotifytest.Playlist{Name: "Stale"})
			if tt.failCurrent {
				fake.SetStatus("DELETE /me/library", http.StatusNotFound)
			}

			if err := spotifyio.UnfollowPlaylist(
				t.Context(),
				slog.New(slog.DiscardHandler),
				fake.Client(),
				spotify.ID(id),
			); err != nil {
				t.Fatalf("UnfollowPlaylist: %v", err)
			}

			if _, ok := fake.PlaylistByName("Stale"); ok {
				t.Error("playlist still followed")
			}
			if !slices.Contains(fake.Requests(), tt.wantRequest) {
				t.Errorf("requests = %v, want %q", fake.Requests(), tt.wantRequest)
			}
		})
	}
}

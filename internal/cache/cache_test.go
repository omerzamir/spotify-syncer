package cache_test

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omerzamir/spotify/v2"
	"golang.org/x/oauth2"

	"spotify-syncer/internal/cache"
)

func newStore(t *testing.T) *cache.Store {
	t.Helper()

	store, err := cache.NewStore(filepath.Join(t.TempDir(), "state"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func TestStoreLoadsZeroValuesWhenFilesAreMissing(t *testing.T) {
	store := newStore(t)

	token, err := store.LoadToken()
	if err != nil || token != nil {
		t.Errorf("LoadToken() = %v, %v; want nil, nil", token, err)
	}

	checkpoint, err := store.LoadCheckpoint()
	if err != nil || !checkpoint.LatestAddedAt.IsZero() {
		t.Errorf("LoadCheckpoint() = %+v, %v; want zero, nil", checkpoint, err)
	}

	isrcs, err := store.LoadTrackISRC()
	if err != nil || isrcs != nil {
		t.Errorf("LoadTrackISRC() = %v, %v; want nil, nil", isrcs, err)
	}
}

func TestStoreRoundTrips(t *testing.T) {
	store := newStore(t)

	token := &oauth2.Token{AccessToken: "access", RefreshToken: "refresh"}
	if err := store.SaveToken(token); err != nil {
		t.Fatal(err)
	}
	gotToken, err := store.LoadToken()
	if err != nil || gotToken.AccessToken != "access" || gotToken.RefreshToken != "refresh" {
		t.Errorf("LoadToken() = %+v, %v", gotToken, err)
	}

	checkpoint := cache.Checkpoint{LatestAddedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)}
	if err = store.SaveCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	gotCheckpoint, err := store.LoadCheckpoint()
	if err != nil || !gotCheckpoint.LatestAddedAt.Equal(checkpoint.LatestAddedAt) {
		t.Errorf("LoadCheckpoint() = %+v, %v; want %+v", gotCheckpoint, err, checkpoint)
	}

	genres := map[string]cache.ArtistGenreEntry{"a": {Genres: []string{"rock"}, FetchedAt: checkpoint.LatestAddedAt}}
	if err = store.SaveArtistGenres(genres); err != nil {
		t.Fatal(err)
	}
	gotGenres, err := store.LoadArtistGenres()
	if err != nil || len(gotGenres) != 1 || gotGenres["a"].Genres[0] != "rock" {
		t.Errorf("LoadArtistGenres() = %+v, %v", gotGenres, err)
	}
}

func TestStoreEvictsSingleKeys(t *testing.T) {
	store := newStore(t)

	if err := store.SavePlaylists(map[string]string{"keep": "1", "drop": "2"}); err != nil {
		t.Fatal(err)
	}
	if err := store.EvictPlaylist("drop"); err != nil {
		t.Fatalf("EvictPlaylist: %v", err)
	}
	if err := store.EvictPlaylist("absent"); err != nil {
		t.Fatalf("EvictPlaylist(absent): %v", err)
	}

	got, err := store.LoadPlaylists()
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"keep": "1"}; !maps.Equal(got, want) {
		t.Errorf("playlists = %v, want %v", got, want)
	}

	if err = store.EvictTrackISRC("no-file-yet"); err != nil {
		t.Errorf("EvictTrackISRC on missing file: %v", err)
	}
}

func TestStoreEvictMirrorTracksRemovesFile(t *testing.T) {
	store := newStore(t)

	if err := store.SaveMirrorTracks([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := store.EvictMirrorTracks(); err != nil {
		t.Fatal(err)
	}
	if err := store.EvictMirrorTracks(); err != nil {
		t.Errorf("second EvictMirrorTracks: %v", err)
	}

	got, err := store.LoadMirrorTracks()
	if err != nil || got != nil {
		t.Errorf("LoadMirrorTracks() = %v, %v; want nil, nil", got, err)
	}
}

func TestStoreReportsCorruptFiles(t *testing.T) {
	store := newStore(t)

	if err := os.WriteFile(filepath.Join(store.Dir(), "playlists.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := store.LoadPlaylists(); err == nil {
		t.Error("LoadPlaylists() error = nil, want decode error")
	}
}

func TestIsCacheStaleError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "plain error", err: errors.New("boom"), want: false},
		{name: "404", err: spotify.Error{Status: http.StatusNotFound}, want: true},
		{name: "wrapped 404", err: fmt.Errorf("ctx: %w", spotify.Error{Status: http.StatusNotFound}), want: true},
		{name: "400 invalid id", err: spotify.Error{Status: http.StatusBadRequest, Message: "Invalid ID"}, want: true},
		{name: "400 other", err: spotify.Error{Status: http.StatusBadRequest, Message: "bad request"}, want: false},
		{name: "500", err: spotify.Error{Status: http.StatusInternalServerError}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cache.IsCacheStaleError(tt.err); got != tt.want {
				t.Errorf("IsCacheStaleError(%v) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
}

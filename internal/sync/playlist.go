// Package sync provides synchronization logic for mirror and genre playlists.
package sync

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/omerzamir/spotify/v2"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/spotifyio"
)

// handleStalePlaylistCache handles stale playlist cache by evicting and logging.
func handleStalePlaylistCache(
	ctx context.Context,
	logger *slog.Logger,
	store *cache.Store,
	name string,
	cachedID string,
	playlists map[string]string,
) {
	logger.WarnContext(ctx, "cached playlist ID is stale, evicting and re-fetching",
		"playlist_name", name,
		"cached_id", cachedID)
	delete(playlists, name)
	if evictErr := store.EvictPlaylist(name); evictErr != nil {
		logger.WarnContext(ctx, "failed to evict stale playlist cache", "error", evictErr)
	}
}

// findOrCreatePlaylist finds a playlist by name using the cache, or creates it
// if not found. Supports cache fallback if the cached playlist ID is stale (404).
// If cachedPlaylists is provided, updates it in place. Otherwise, loads and saves
// the playlists cache separately.
//
// The returned bool reports whether the playlist ID came from the cache and was
// validated against the API. When it is false the playlist was looked up by name
// or newly created, so any cached track contents describe a different playlist
// and must not be trusted.
func findOrCreatePlaylist(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	store *cache.Store,
	name string,
	cachedPlaylists map[string]string,
) (spotify.ID, bool, error) {
	var playlists map[string]string
	var updateInPlace bool

	if cachedPlaylists != nil {
		playlists = cachedPlaylists
		updateInPlace = true
	} else {
		var err error
		playlists, err = store.LoadPlaylists()
		if err != nil {
			return "", false, fmt.Errorf("loading playlists cache: %w", err)
		}
		if playlists == nil {
			playlists = make(map[string]string)
		}
	}

	cachedID, found := playlists[name]
	if found {
		playlistID := spotify.ID(cachedID)
		_, getErr := client.GetPlaylist(ctx, playlistID)
		if getErr == nil {
			return playlistID, true, nil
		}

		if cache.IsCacheStaleError(getErr) {
			handleStalePlaylistCache(ctx, logger, store, name, cachedID, playlists)
		} else {
			return "", false, fmt.Errorf("checking cached playlist: %w", getErr)
		}
	}

	playlistID, createErr := spotifyio.FindOrCreatePlaylist(
		ctx,
		logger,
		client,
		name,
		"",
		true,
	)
	if createErr != nil {
		return "", false, fmt.Errorf("finding or creating playlist: %w", createErr)
	}

	playlists[name] = string(playlistID)

	if !updateInPlace {
		if saveErr := store.SavePlaylists(playlists); saveErr != nil {
			logger.WarnContext(ctx, "failed to save playlist cache", "error", saveErr)
		}
	}

	return playlistID, false, nil
}

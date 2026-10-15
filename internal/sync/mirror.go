// Package sync provides synchronization logic for mirror and genre playlists.
package sync

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/omerzamir/spotify/v2"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/config"
)

// MirrorResult holds the result of synchronizing the mirror playlist.
type MirrorResult struct {
	Added   int
	Removed int
}

// Mirror synchronizes the mirror playlist with the desired track set using
// set difference operations. It loads cached mirror tracks, computes the diff,
// applies batch add/remove operations, and updates the cache. Supports cache
// fallback if cached data is stale.
func Mirror(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.SyncConfig,
	client *spotify.Client,
	store *cache.Store,
	desiredTracks []spotify.ID,
	forceRefresh bool,
) (MirrorResult, error) {
	playlistID, cachedID, err := findOrCreatePlaylist(
		ctx,
		logger,
		client,
		store,
		cfg.MirrorPlaylistName,
		nil,
	)
	if err != nil {
		return MirrorResult{}, fmt.Errorf("finding or creating mirror playlist: %w", err)
	}

	if !cachedID {
		// The playlist was recreated or re-resolved by name, so the cached
		// contents describe a different playlist.
		logger.InfoContext(ctx, "mirror playlist is not the cached one, discarding cached mirror tracks",
			"playlist_id", playlistID)
		if evictErr := store.EvictMirrorTracks(); evictErr != nil {
			return MirrorResult{}, fmt.Errorf("evicting stale mirror tracks cache: %w", evictErr)
		}
	}

	syncCfg := syncConfig{
		batchSize:     cfg.BatchSize,
		playlistID:    playlistID,
		desiredTracks: desiredTracks,
		forceRefresh:  forceRefresh,
		logContext:    "mirror",
		evictCache: func() error {
			return store.EvictMirrorTracks()
		},
		saveCache: func(tracks []spotify.ID) error {
			return store.SaveMirrorTracks(spotifyIDsToStringSlice(tracks))
		},
		loadCache: func() ([]spotify.ID, error) {
			cached, loadErr := store.LoadMirrorTracks()
			if loadErr != nil {
				return nil, loadErr
			}
			if len(cached) == 0 {
				return nil, nil
			}
			return stringSliceToSpotifyIDs(cached), nil
		},
	}

	result, err := syncPlaylist(ctx, logger, client, syncCfg)
	if err != nil {
		return MirrorResult{}, err
	}

	return MirrorResult(result), nil
}

// spotifyIDsToStringSlice converts a slice of spotify.ID to a slice of strings.
func spotifyIDsToStringSlice(ids []spotify.ID) []string {
	result := make([]string, len(ids))
	for i, id := range ids {
		result[i] = string(id)
	}
	return result
}

// stringSliceToSpotifyIDs converts a slice of strings to a slice of spotify.ID.
func stringSliceToSpotifyIDs(strs []string) []spotify.ID {
	result := make([]spotify.ID, len(strs))
	for i, s := range strs {
		result[i] = spotify.ID(s)
	}
	return result
}

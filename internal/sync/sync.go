// Package sync provides synchronization logic for mirror and genre playlists.
package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/zmb3/spotify/v2"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/set"
	"spotify-syncer/internal/spotifyio"
)

// syncConfig holds configuration for syncing a playlist.
type syncConfig struct {
	batchSize     int
	playlistID    spotify.ID
	desiredTracks []spotify.ID
	forceRefresh  bool
	logContext    string
	evictCache    func() error
	saveCache     func([]spotify.ID) error
	loadCache     func() ([]spotify.ID, error)
}

// syncResult holds the result of syncing a playlist.
type syncResult struct {
	Added   int
	Removed int
}

// syncPlaylist synchronizes a playlist with its desired track set. It computes
// the diff against the cached (or fetched) current tracks, applies batch
// add/remove operations, and updates the cache. If the cached contents turn
// out to be stale, it re-fetches the playlist and retries once.
func syncPlaylist(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	cfg syncConfig,
) (syncResult, error) {
	currentTracks, err := loadCurrentTracks(ctx, logger, client, cfg)
	if err != nil {
		return syncResult{}, err
	}

	result, err := applyDiff(ctx, logger, client, cfg, currentTracks)
	if err != nil {
		if !cache.IsCacheStaleError(err) {
			return syncResult{}, err
		}

		logger.WarnContext(ctx, "stale cache detected, falling back to API fetch",
			"playlist", cfg.logContext,
			"error", err)

		return syncPlaylistWithFallback(ctx, logger, client, cfg)
	}

	logger.InfoContext(ctx, "playlist sync complete",
		"playlist", cfg.logContext,
		"added", result.Added,
		"removed", result.Removed)

	return result, nil
}

// syncPlaylistWithFallback evicts the playlist cache, fetches the current
// tracks from the API, and applies the diff against them.
func syncPlaylistWithFallback(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	cfg syncConfig,
) (syncResult, error) {
	if cfg.evictCache != nil {
		if evictErr := cfg.evictCache(); evictErr != nil {
			logger.WarnContext(ctx, "failed to evict tracks cache",
				"playlist", cfg.logContext,
				"error", evictErr)
		}
	}

	fetched, err := spotifyio.FetchPlaylistTrackIDs(ctx, logger, client, cfg.playlistID)
	if err != nil {
		return syncResult{}, fmt.Errorf("fetching fresh %s playlist tracks: %w", cfg.logContext, err)
	}

	return applyDiff(ctx, logger, client, cfg, fetched)
}

// applyDiff adds the desired tracks missing from current, removes the current
// tracks that are no longer desired, and saves the desired set to the cache.
// Errors from the API are wrapped with %w so callers can detect stale caches.
func applyDiff(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	cfg syncConfig,
	current []spotify.ID,
) (syncResult, error) {
	toAdd, toRemove := diffTracks(cfg.desiredTracks, current)

	logger.InfoContext(ctx, "computed playlist diff",
		"playlist", cfg.logContext,
		"current_tracks", len(current),
		"desired_tracks", len(cfg.desiredTracks),
		"to_add", len(toAdd),
		"to_remove", len(toRemove))

	if err := spotifyio.BatchAddTracks(ctx, logger, client, cfg.playlistID, toAdd, cfg.batchSize); err != nil {
		return syncResult{}, fmt.Errorf("adding tracks to %s playlist: %w", cfg.logContext, err)
	}

	if err := spotifyio.BatchRemoveTracks(ctx, logger, client, cfg.playlistID, toRemove, cfg.batchSize); err != nil {
		return syncResult{}, fmt.Errorf("removing tracks from %s playlist: %w", cfg.logContext, err)
	}

	if err := cfg.saveCache(cfg.desiredTracks); err != nil {
		return syncResult{}, fmt.Errorf("saving %s tracks cache: %w", cfg.logContext, err)
	}

	return syncResult{Added: len(toAdd), Removed: len(toRemove)}, nil
}

// diffTracks returns the desired tracks missing from current (in desired
// order) and the current tracks not desired (in current order). Ordering
// matters: added tracks land in the playlist in the same order as desired.
func diffTracks(desired, current []spotify.ID) ([]spotify.ID, []spotify.ID) {
	toAdd := set.Missing(desired, set.Of(current...))
	toRemove := set.Missing(current, set.Of(desired...))
	return toAdd, toRemove
}

// loadCurrentTracks loads current tracks from cache or API based on forceRefresh flag.
func loadCurrentTracks(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	cfg syncConfig,
) ([]spotify.ID, error) {
	if cfg.forceRefresh {
		logger.InfoContext(ctx, fmt.Sprintf("force refresh enabled, fetching %s playlist from API", cfg.logContext))
		fetched, fetchErr := spotifyio.FetchPlaylistTrackIDs(ctx, logger, client, cfg.playlistID)
		if fetchErr != nil {
			return nil, fmt.Errorf("fetching %s playlist tracks: %w", cfg.logContext, fetchErr)
		}
		return fetched, nil
	}

	if cfg.loadCache == nil {
		return nil, errors.New("loadCache function not provided")
	}

	cached, loadErr := cfg.loadCache()
	if loadErr != nil {
		return nil, fmt.Errorf("loading cached %s tracks: %w", cfg.logContext, loadErr)
	}

	if len(cached) == 0 {
		logger.InfoContext(ctx, fmt.Sprintf("no cached %s tracks, fetching from API", cfg.logContext))
		fetched, fetchErr := spotifyio.FetchPlaylistTrackIDs(ctx, logger, client, cfg.playlistID)
		if fetchErr != nil {
			return nil, fmt.Errorf("fetching %s playlist tracks: %w", cfg.logContext, fetchErr)
		}
		return fetched, nil
	}

	return cached, nil
}

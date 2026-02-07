// Package sync provides synchronization logic for mirror and genre playlists.
package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/zmb3/spotify/v2"

	"spotify-syncer/internal/cache"
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
// the diff, applies batch add/remove operations, and updates the cache.
// Supports cache fallback if cached data is stale.
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

	desiredSet := mapset.NewSet(cfg.desiredTracks...)
	currentSet := mapset.NewSet(currentTracks...)

	toAdd := desiredSet.Difference(currentSet).ToSlice()
	toRemove := currentSet.Difference(desiredSet).ToSlice()

	logger.InfoContext(ctx, fmt.Sprintf("computed %s playlist diff", cfg.logContext),
		"current_tracks", currentSet.Cardinality(),
		"desired_tracks", desiredSet.Cardinality(),
		"to_add", len(toAdd),
		"to_remove", len(toRemove))

	if len(toAdd) > 0 {
		err = spotifyio.BatchAddTracks(
			ctx,
			logger,
			client,
			cfg.playlistID,
			toAdd,
			cfg.batchSize,
		)
		if err != nil {
			if cache.IsCacheStaleError(err) {
				logger.WarnContext(
					ctx,
					fmt.Sprintf("stale cache detected during add, falling back to API fetch for %s", cfg.logContext),
					"error",
					err,
				)
				return syncPlaylistWithFallback(ctx, logger, client, cfg)
			}
			return syncResult{}, fmt.Errorf("adding tracks to %s playlist: %w", cfg.logContext, err)
		}
	}

	if len(toRemove) > 0 {
		err = spotifyio.BatchRemoveTracks(
			ctx,
			logger,
			client,
			cfg.playlistID,
			toRemove,
			cfg.batchSize,
		)
		if err != nil {
			if cache.IsCacheStaleError(err) {
				logger.WarnContext(
					ctx,
					fmt.Sprintf("stale cache detected during remove, falling back to API fetch for %s", cfg.logContext),
					"error",
					err,
				)
				return syncPlaylistWithFallback(ctx, logger, client, cfg)
			}
			return syncResult{}, fmt.Errorf("removing tracks from %s playlist: %w", cfg.logContext, err)
		}
	}

	if saveErr := cfg.saveCache(cfg.desiredTracks); saveErr != nil {
		return syncResult{}, fmt.Errorf("saving %s tracks cache: %w", cfg.logContext, saveErr)
	}

	logger.InfoContext(ctx, fmt.Sprintf("%s playlist sync complete", cfg.logContext),
		"added", len(toAdd),
		"removed", len(toRemove))

	return syncResult{
		Added:   len(toAdd),
		Removed: len(toRemove),
	}, nil
}

// syncPlaylistWithFallback handles cache fallback: evicts cache, fetches fresh
// data, and retries the sync operation.
func syncPlaylistWithFallback(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	cfg syncConfig,
) (syncResult, error) {
	if cfg.evictCache != nil {
		if evictErr := cfg.evictCache(); evictErr != nil {
			logger.WarnContext(ctx, fmt.Sprintf("failed to evict %s tracks cache", cfg.logContext), "error", evictErr)
		}
	}

	fetched, fetchErr := spotifyio.FetchPlaylistTrackIDs(ctx, logger, client, cfg.playlistID)
	if fetchErr != nil {
		return syncResult{}, fmt.Errorf("fetching fresh %s playlist tracks: %w", cfg.logContext, fetchErr)
	}

	desiredSet := mapset.NewSet(cfg.desiredTracks...)
	currentSet := mapset.NewSet(fetched...)

	toAdd := desiredSet.Difference(currentSet).ToSlice()
	toRemove := currentSet.Difference(desiredSet).ToSlice()

	if len(toAdd) > 0 {
		addErr := spotifyio.BatchAddTracks(
			ctx,
			logger,
			client,
			cfg.playlistID,
			toAdd,
			cfg.batchSize,
		)
		if addErr != nil {
			return syncResult{}, fmt.Errorf("adding tracks after fallback: %w", addErr)
		}
	}

	if len(toRemove) > 0 {
		removeErr := spotifyio.BatchRemoveTracks(
			ctx,
			logger,
			client,
			cfg.playlistID,
			toRemove,
			cfg.batchSize,
		)
		if removeErr != nil {
			return syncResult{}, fmt.Errorf("removing tracks after fallback: %w", removeErr)
		}
	}

	if saveErr := cfg.saveCache(cfg.desiredTracks); saveErr != nil {
		return syncResult{}, fmt.Errorf("saving %s tracks cache after fallback: %w", cfg.logContext, saveErr)
	}

	return syncResult{
		Added:   len(toAdd),
		Removed: len(toRemove),
	}, nil
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

// Package sync provides synchronization logic for mirror and genre playlists.
package sync

import (
	"context"
	"fmt"
	"log/slog"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/zmb3/spotify/v2"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/config"
	"spotify-syncer/internal/spotifyio"
)

// GenreSyncResult holds the result of synchronizing genre playlists.
type GenreSyncResult struct {
	BucketsSynced  int
	BucketsRemoved int
	TotalAdded     int
	TotalRemoved   int
}

// Genres synchronizes all genre bucket playlists with their desired track
// sets. For each bucket, it loads cached tracks, computes the diff, applies
// batch add/remove operations, and updates the cache. Detects and cleans up
// stale genre playlists that are no longer selected.
func Genres(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.Config,
	client *spotify.Client,
	store *cache.Store,
	buckets map[string][]spotify.ID,
	forceRefresh bool,
) (GenreSyncResult, error) {
	cachedBuckets, err := store.LoadBucketTracks()
	if err != nil {
		return GenreSyncResult{}, fmt.Errorf("loading cached bucket tracks: %w", err)
	}

	cachedPlaylists, loadErr := store.LoadPlaylists()
	if loadErr != nil {
		return GenreSyncResult{}, fmt.Errorf("loading cached playlists: %w", loadErr)
	}

	if cachedPlaylists == nil {
		cachedPlaylists = make(map[string]string)
	}

	result := GenreSyncResult{
		BucketsSynced: len(buckets),
	}

	for bucketName, desiredTracks := range buckets {
		bucketResult, syncErr := syncBucket(
			ctx,
			logger,
			cfg,
			client,
			store,
			bucketName,
			desiredTracks,
			cachedBuckets,
			cachedPlaylists,
			forceRefresh,
		)
		if syncErr != nil {
			return GenreSyncResult{}, fmt.Errorf("syncing bucket %q: %w", bucketName, syncErr)
		}

		result.TotalAdded += bucketResult.Added
		result.TotalRemoved += bucketResult.Removed
	}

	staleBuckets := findStaleBuckets(buckets, cachedBuckets)
	for _, staleBucketName := range staleBuckets {
		if cleanupErr := cleanupStaleBucket(
			ctx,
			logger,
			cfg,
			client,
			store,
			staleBucketName,
			cachedPlaylists,
		); cleanupErr != nil {
			logger.WarnContext(ctx, "failed to cleanup stale bucket",
				"bucket", staleBucketName,
				"error", cleanupErr)
			continue
		}
		result.BucketsRemoved++
	}

	if saveErr := store.SavePlaylists(cachedPlaylists); saveErr != nil {
		logger.WarnContext(ctx, "failed to save playlists cache", "error", saveErr)
	}

	logger.InfoContext(ctx, "genre sync complete",
		"buckets_synced", result.BucketsSynced,
		"buckets_removed", result.BucketsRemoved,
		"total_added", result.TotalAdded,
		"total_removed", result.TotalRemoved)

	return result, nil
}

// bucketSyncResult holds the result of syncing a single bucket.
type bucketSyncResult struct {
	Added   int
	Removed int
}

// syncBucket synchronizes a single genre bucket playlist with its desired tracks.
func syncBucket(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.Config,
	client *spotify.Client,
	store *cache.Store,
	bucketName string,
	desiredTracks []spotify.ID,
	cachedBuckets map[string][]string,
	cachedPlaylists map[string]string,
	forceRefresh bool,
) (bucketSyncResult, error) {
	playlistName := cfg.Genre.BucketPlaylistPrefix + bucketName
	playlistID, err := findOrCreatePlaylist(
		ctx,
		logger,
		client,
		store,
		playlistName,
		cachedPlaylists,
	)
	if err != nil {
		return bucketSyncResult{}, fmt.Errorf("finding or creating playlist: %w", err)
	}

	syncCfg := syncConfig{
		batchSize:     cfg.Sync.BatchSize,
		playlistID:    playlistID,
		desiredTracks: desiredTracks,
		forceRefresh:  forceRefresh,
		logContext:    "bucket",
		evictCache: func() error {
			return store.EvictBucketTracks(bucketName)
		},
		saveCache: func(tracks []spotify.ID) error {
			if cachedBuckets == nil {
				cachedBuckets = make(map[string][]string)
			}
			cachedBuckets[bucketName] = spotifyIDsToStringSlice(tracks)
			return store.SaveBucketTracks(cachedBuckets)
		},
		loadCache: func() ([]spotify.ID, error) {
			cached, ok := cachedBuckets[bucketName]
			if !ok || len(cached) == 0 {
				return nil, nil
			}
			return stringSliceToSpotifyIDs(cached), nil
		},
	}

	result, err := syncPlaylist(ctx, logger, client, syncCfg)
	if err != nil {
		return bucketSyncResult{}, err
	}

	return bucketSyncResult(result), nil
}

// findStaleBuckets identifies bucket names that exist in the cache but are
// no longer in the current buckets map (i.e., genres that dropped below
// min_bucket_size threshold).
func findStaleBuckets(
	currentBuckets map[string][]spotify.ID,
	cachedBuckets map[string][]string,
) []string {
	currentSet := mapset.NewSet[string]()
	for bucketName := range currentBuckets {
		currentSet.Add(bucketName)
	}

	var stale []string
	for bucketName := range cachedBuckets {
		if !currentSet.Contains(bucketName) {
			stale = append(stale, bucketName)
		}
	}

	return stale
}

// handleStaleBucketPlaylist handles the case where a stale bucket playlist is already deleted.
func handleStaleBucketPlaylist(
	ctx context.Context,
	logger *slog.Logger,
	store *cache.Store,
	bucketName string,
	playlistName string,
	cachedPlaylists map[string]string,
) error {
	logger.InfoContext(ctx, "stale bucket playlist already deleted, evicting from cache",
		"bucket", bucketName)
	delete(cachedPlaylists, playlistName)
	if evictErr := store.EvictPlaylist(playlistName); evictErr != nil {
		logger.WarnContext(ctx, "failed to evict stale playlist cache", "error", evictErr)
	}
	if evictErr := store.EvictBucketTracks(bucketName); evictErr != nil {
		logger.WarnContext(ctx, "failed to evict bucket tracks cache", "error", evictErr)
	}
	return nil
}

// cleanupStaleBucket removes all tracks from a stale bucket playlist, unfollows
// (deletes) it, and evicts it from all caches. Updates cachedPlaylists map in place.
func cleanupStaleBucket(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.Config,
	client *spotify.Client,
	store *cache.Store,
	bucketName string,
	cachedPlaylists map[string]string,
) error {
	playlistName := cfg.Genre.BucketPlaylistPrefix + bucketName
	playlistIDStr, found := cachedPlaylists[playlistName]
	if !found {
		logger.InfoContext(ctx, "stale bucket playlist not in cache, skipping cleanup",
			"bucket", bucketName)
		return nil
	}

	playlistID := spotify.ID(playlistIDStr)

	logger.InfoContext(ctx, "cleaning up stale bucket playlist",
		"bucket", bucketName,
		"playlist_id", playlistID)

	currentTracks, err := spotifyio.FetchPlaylistTrackIDs(ctx, logger, client, playlistID)
	if err != nil {
		if cache.IsCacheStaleError(err) {
			return handleStaleBucketPlaylist(ctx, logger, store, bucketName, playlistName, cachedPlaylists)
		}
		return fmt.Errorf("fetching stale playlist tracks: %w", err)
	}

	if len(currentTracks) > 0 {
		err = spotifyio.BatchRemoveTracks(
			ctx,
			logger,
			client,
			playlistID,
			currentTracks,
			cfg.Sync.BatchSize,
		)
		if err != nil {
			return fmt.Errorf("removing tracks from stale playlist: %w", err)
		}
	}

	err = spotifyio.UnfollowPlaylist(ctx, logger, client, playlistID)
	if err != nil {
		return fmt.Errorf("unfollowing stale playlist: %w", err)
	}

	delete(cachedPlaylists, playlistName)
	if evictErr := store.EvictPlaylist(playlistName); evictErr != nil {
		logger.WarnContext(ctx, "failed to evict stale playlist cache", "error", evictErr)
	}

	cachedBuckets, err := store.LoadBucketTracks()
	if err == nil {
		delete(cachedBuckets, bucketName)
		if saveErr := store.SaveBucketTracks(cachedBuckets); saveErr != nil {
			logger.WarnContext(ctx, "failed to save bucket tracks after cleanup", "error", saveErr)
		}
	} else {
		if evictErr := store.EvictBucketTracks(bucketName); evictErr != nil {
			logger.WarnContext(ctx, "failed to evict bucket tracks cache", "error", evictErr)
		}
	}

	logger.InfoContext(ctx, "stale bucket playlist cleanup complete",
		"bucket", bucketName)

	return nil
}

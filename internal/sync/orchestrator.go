// Package sync provides synchronization logic for mirror and genre playlists.
package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/zmb3/spotify/v2"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/classifier"
	"spotify-syncer/internal/config"
	"spotify-syncer/internal/dedup"
	"spotify-syncer/internal/set"
	"spotify-syncer/internal/spotifyio"
)

// RunResult holds the result of a complete sync run.
type RunResult struct {
	Metrics cache.RunMetrics
	Success bool
}

// Run orchestrates the complete sync workflow: fetch liked tracks, deduplicate,
// sync mirror playlist, fetch artist genres, classify tracks, sync genre playlists,
// and save metrics. Returns a RunResult with success status and metrics.
func Run(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.Config,
	client *spotify.Client,
	store *cache.Store,
	enableGenres bool,
	forceRefresh bool,
) (RunResult, error) {
	metrics := cache.RunMetrics{
		StartedAt: time.Now(),
	}

	logger.InfoContext(ctx, "starting sync run",
		"genres_enabled", enableGenres,
		"force_refresh", forceRefresh)

	likedTracks, trackArtists, dedupResult, err := fetchAndDeduplicateTracks(ctx, logger, client, store)
	if err != nil {
		return RunResult{}, err
	}

	updateDedupMetrics(&metrics, likedTracks, dedupResult)

	mirrorResult, err := syncMirrorPlaylist(
		ctx,
		logger,
		cfg,
		client,
		store,
		dedupResult.RepresentativeTracks,
		forceRefresh,
	)
	if err != nil {
		return RunResult{}, err
	}

	metrics.MirrorAdded = mirrorResult.Added
	metrics.MirrorRemoved = mirrorResult.Removed

	if shouldSkipGenreSync(enableGenres, mirrorResult, forceRefresh) {
		metrics.BucketsCreated = 0
		metrics.BucketsRemoved = 0
		return completeRun(
			ctx,
			logger,
			store,
			&metrics,
			"sync run complete (genre sync skipped, no changes detected)",
			false,
		), nil
	}

	if !enableGenres {
		metrics.BucketsCreated = 0
		metrics.BucketsRemoved = 0
		return completeRun(ctx, logger, store, &metrics, "sync run complete (genres disabled)", false), nil
	}

	genreResult, err := syncGenrePlaylists(
		ctx,
		logger,
		cfg,
		client,
		store,
		dedupResult.RepresentativeTracks,
		trackArtists,
		forceRefresh,
	)
	if err != nil {
		return RunResult{}, err
	}

	metrics.BucketsCreated = genreResult.BucketsCreated
	metrics.BucketsRemoved = genreResult.BucketsRemoved
	return completeRun(ctx, logger, store, &metrics, "sync run complete", true), nil
}

// fetchAndDeduplicateTracks fetches liked tracks, builds track-artist map, and deduplicates tracks.
func fetchAndDeduplicateTracks(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	store *cache.Store,
) ([]spotifyio.LikedTrackData, map[string][]string, dedup.DeduplicationResult, error) {
	likedTracks, err := spotifyio.FetchAllLikedTracks(ctx, logger, client)
	if err != nil {
		return nil, nil, dedup.DeduplicationResult{}, fmt.Errorf("fetching liked tracks: %w", err)
	}

	trackArtists, err := buildTrackArtistsMap(likedTracks, store)
	if err != nil {
		return nil, nil, dedup.DeduplicationResult{}, fmt.Errorf("building track-artist map: %w", err)
	}

	dedupResult, err := dedup.DeduplicateTracks(ctx, logger, store, likedTracks)
	if err != nil {
		return nil, nil, dedup.DeduplicationResult{}, fmt.Errorf("deduplicating tracks: %w", err)
	}

	return likedTracks, trackArtists, dedupResult, nil
}

// updateDedupMetrics updates metrics with deduplication results.
func updateDedupMetrics(
	metrics *cache.RunMetrics,
	likedTracks []spotifyio.LikedTrackData,
	dedupResult dedup.DeduplicationResult,
) {
	metrics.LikedTotal = len(likedTracks)
	metrics.DedupRemoved = len(dedupResult.DuplicateIDs)
	duplicateIDStrings := make([]string, len(dedupResult.DuplicateIDs))
	for i, id := range dedupResult.DuplicateIDs {
		duplicateIDStrings[i] = string(id)
	}
	metrics.DuplicateIDs = duplicateIDStrings
}

// syncMirrorPlaylist syncs the mirror playlist and returns the result.
func syncMirrorPlaylist(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.Config,
	client *spotify.Client,
	store *cache.Store,
	desiredTracks []spotify.ID,
	forceRefresh bool,
) (MirrorResult, error) {
	mirrorResult, err := Mirror(
		ctx,
		logger,
		cfg.Sync,
		client,
		store,
		desiredTracks,
		forceRefresh,
	)
	if err != nil {
		return MirrorResult{}, fmt.Errorf("syncing mirror playlist: %w", err)
	}
	return mirrorResult, nil
}

// bucketCounts reports how many genre playlists a sync created and removed.
type bucketCounts struct {
	BucketsCreated int
	BucketsRemoved int
}

// syncGenrePlaylists syncs genre playlists by fetching artist genres, classifying tracks, and syncing buckets.
func syncGenrePlaylists(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.Config,
	client *spotify.Client,
	store *cache.Store,
	trackIDs []spotify.ID,
	trackArtists map[string][]string,
	forceRefresh bool,
) (bucketCounts, error) {
	artistGenres, err := fetchArtistGenres(
		ctx,
		logger,
		cfg,
		client,
		store,
		trackIDs,
		trackArtists,
	)
	if err != nil {
		return bucketCounts{}, fmt.Errorf("fetching artist genres: %w", err)
	}

	classifyResult, err := classifier.Classify(
		ctx,
		logger,
		cfg.Genre,
		trackIDs,
		trackArtists,
		artistGenres,
	)
	if err != nil {
		return bucketCounts{}, fmt.Errorf("classifying tracks: %w", err)
	}

	genreResult, err := Genres(
		ctx,
		logger,
		cfg,
		client,
		store,
		classifyResult.Buckets,
		forceRefresh,
	)
	if err != nil {
		return bucketCounts{}, fmt.Errorf("syncing genre playlists: %w", err)
	}

	return bucketCounts{
		BucketsCreated: len(classifyResult.Buckets),
		BucketsRemoved: genreResult.BucketsRemoved,
	}, nil
}

// shouldSkipGenreSync determines if genre sync should be skipped based on conditions.
func shouldSkipGenreSync(enableGenres bool, mirrorResult MirrorResult, forceRefresh bool) bool {
	return enableGenres && mirrorResult.Added == 0 && mirrorResult.Removed == 0 && !forceRefresh
}

// completeRun finalizes the sync run by setting FinishedAt, saving metrics,
// logging completion, and returning a RunResult. The includeBuckets parameter
// controls whether bucket metrics are included in the log output.
func completeRun(
	ctx context.Context,
	logger *slog.Logger,
	store *cache.Store,
	metrics *cache.RunMetrics,
	logMessage string,
	includeBuckets bool,
) RunResult {
	metrics.FinishedAt = time.Now()

	if err := store.SaveMetrics(*metrics); err != nil {
		logger.WarnContext(ctx, "failed to save metrics", "error", err)
	}

	attrs := []slog.Attr{
		slog.Int("liked_total", metrics.LikedTotal),
		slog.Int("dedup_removed", metrics.DedupRemoved),
		slog.Int("mirror_added", metrics.MirrorAdded),
		slog.Int("mirror_removed", metrics.MirrorRemoved),
	}

	if includeBuckets {
		attrs = append(attrs,
			slog.Int("buckets_created", metrics.BucketsCreated),
			slog.Int("buckets_removed", metrics.BucketsRemoved),
		)
	}

	attrs = append(attrs, slog.Duration("duration", metrics.FinishedAt.Sub(metrics.StartedAt)))

	logger.LogAttrs(ctx, slog.LevelInfo, logMessage, attrs...)

	return RunResult{
		Metrics: *metrics,
		Success: true,
	}
}

// buildTrackArtistsMap builds a map from track ID to artist IDs, merging with
// cached data and saving updates. Returns the complete map.
func buildTrackArtistsMap(
	tracks []spotifyio.LikedTrackData,
	store *cache.Store,
) (map[string][]string, error) {
	cached, err := store.LoadTrackArtists()
	if err != nil {
		return nil, fmt.Errorf("loading cached track-artist map: %w", err)
	}

	if cached == nil {
		cached = make(map[string][]string)
	}

	updated := false
	for _, track := range tracks {
		trackIDStr := string(track.TrackID)
		artistIDStrs := make([]string, len(track.ArtistIDs))
		for i, artistID := range track.ArtistIDs {
			artistIDStrs[i] = string(artistID)
		}

		if _, exists := cached[trackIDStr]; !exists {
			cached[trackIDStr] = artistIDStrs
			updated = true
		}
	}

	if updated {
		if saveErr := store.SaveTrackArtists(cached); saveErr != nil {
			return nil, fmt.Errorf("saving track-artist map: %w", saveErr)
		}
	}

	return cached, nil
}

// fetchArtistGenres loads cached artist genres, identifies unknown or expired
// artists from the deduplicated tracks, fetches their genres from the API,
// and returns the complete artist genre map (cached + fresh).
func fetchArtistGenres(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.Config,
	client *spotify.Client,
	store *cache.Store,
	trackIDs []spotify.ID,
	trackArtists map[string][]string,
) (map[string]cache.ArtistGenreEntry, error) {
	cached, err := store.LoadArtistGenres()
	if err != nil {
		return nil, fmt.Errorf("loading cached artist genres: %w", err)
	}

	if cached == nil {
		cached = make(map[string]cache.ArtistGenreEntry)
	}

	allArtistIDs := collectUniqueArtistIDs(trackIDs, trackArtists)
	unknownOrExpired := filterUnknownOrExpiredArtists(
		allArtistIDs,
		cached,
		cfg.Genre.ArtistGenreTTL,
	)

	if len(unknownOrExpired) == 0 {
		logger.InfoContext(ctx, "all artist genres cached and fresh",
			"total_artists", len(allArtistIDs))
		return cached, nil
	}

	logger.InfoContext(ctx, "fetching artist genres from API",
		"total_artists", len(allArtistIDs),
		"unknown_or_expired", len(unknownOrExpired))

	spotifyArtistIDs := make([]spotify.ID, len(unknownOrExpired))
	for i, artistIDStr := range unknownOrExpired {
		spotifyArtistIDs[i] = spotify.ID(artistIDStr)
	}

	artists, err := spotifyio.BatchGetArtists(ctx, logger, client, spotifyArtistIDs)
	if err != nil {
		return nil, fmt.Errorf("fetching artists from API: %w", err)
	}

	now := time.Now()
	updated := false

	for artistID, artist := range artists {
		if artist == nil {
			continue
		}

		artistIDStr := string(artistID)
		genres := make([]string, len(artist.Genres))
		copy(genres, artist.Genres)

		cached[artistIDStr] = cache.ArtistGenreEntry{
			Genres:    genres,
			FetchedAt: now,
		}
		updated = true
	}

	for _, artistIDStr := range unknownOrExpired {
		artistID := spotify.ID(artistIDStr)
		if _, found := artists[artistID]; !found {
			logger.WarnContext(ctx, "artist not found in API response, evicting from cache",
				"artist_id", artistIDStr)
			if evictErr := store.EvictArtistGenre(artistIDStr); evictErr != nil {
				logger.WarnContext(ctx, "failed to evict artist genre", "error", evictErr)
			}
			delete(cached, artistIDStr)
		}
	}

	if updated {
		if saveErr := store.SaveArtistGenres(cached); saveErr != nil {
			return nil, fmt.Errorf("saving artist genres cache: %w", saveErr)
		}
	}

	logger.InfoContext(ctx, "artist genre fetch complete",
		"fetched", len(artists),
		"total_cached", len(cached))

	return cached, nil
}

// collectUniqueArtistIDs collects all unique artist IDs from the deduplicated
// tracks using the track-artist mapping.
func collectUniqueArtistIDs(
	trackIDs []spotify.ID,
	trackArtists map[string][]string,
) []string {
	var artistIDs []string
	for _, trackID := range trackIDs {
		artistIDs = append(artistIDs, trackArtists[string(trackID)]...)
	}

	return set.Missing(artistIDs, set.New[string](0))
}

// filterUnknownOrExpiredArtists returns artist IDs that are either not in the
// cache or have expired based on the TTL.
func filterUnknownOrExpiredArtists(
	artistIDs []string,
	cached map[string]cache.ArtistGenreEntry,
	ttl time.Duration,
) []string {
	now := time.Now()
	var result []string

	for _, artistID := range artistIDs {
		entry, ok := cached[artistID]
		if !ok {
			result = append(result, artistID)
			continue
		}

		if now.Sub(entry.FetchedAt) > ttl {
			result = append(result, artistID)
		}
	}

	return result
}

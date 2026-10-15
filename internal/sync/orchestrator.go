// Package sync provides synchronization logic for mirror and genre playlists.
package sync

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/omerzamir/spotify/v2"

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

// RunOptions controls a sync run.
type RunOptions struct {
	Mode         Mode
	EnableGenres bool
	ForceRefresh bool
}

// runPlan is the target state a sync run reconciles the playlists towards.
type runPlan struct {
	mode Mode

	// desiredTracks is the mirror playlist content, oldest liked first, so
	// tracks liked later are appended at the end in both modes.
	desiredTracks []spotify.ID
	trackArtists  map[string][]string
	duplicateIDs  []spotify.ID
	likedTotal    int
	newTracks     int

	// checkpoint is saved only after the run succeeds.
	checkpoint cache.Checkpoint

	// refreshExpiredGenres re-fetches artist genres older than the TTL.
	// Fast runs only fetch genres for artists not seen before.
	refreshExpiredGenres bool
}

// Run orchestrates a sync run: it builds the desired state for the selected
// mode, syncs the mirror playlist, fetches artist genres, classifies tracks,
// syncs genre playlists, and saves the checkpoint and metrics.
//
// Fast mode only adds tracks liked since the last checkpoint and falls back
// to full mode when there is no usable sync state or ForceRefresh is set.
// Tracks unliked since the last full run are only removed by full mode.
func Run(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.Config,
	client *spotify.Client,
	store *cache.Store,
	opts RunOptions,
) (RunResult, error) {
	startedAt := time.Now()

	logger.InfoContext(ctx, "starting sync run",
		"mode", opts.Mode,
		"genres_enabled", opts.EnableGenres,
		"force_refresh", opts.ForceRefresh)

	plan, err := planRun(ctx, logger, client, store, opts)
	if err != nil {
		return RunResult{}, err
	}

	return executePlan(ctx, logger, cfg, client, store, opts, plan, startedAt)
}

// planRun builds the run plan for opts.Mode, falling back from fast to full
// when fast mode cannot run.
func planRun(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	store *cache.Store,
	opts RunOptions,
) (runPlan, error) {
	if opts.Mode == ModeFast {
		plan, ok, err := planFastRun(ctx, logger, client, store, opts.ForceRefresh)
		if err != nil || ok {
			return plan, err
		}
	}

	return planFullRun(ctx, logger, client, store)
}

// planFullRun fetches every liked track and deduplicates them by ISRC.
func planFullRun(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	store *cache.Store,
) (runPlan, error) {
	likedTracks, err := spotifyio.FetchAllLikedTracks(ctx, logger, client)
	if err != nil {
		return runPlan{}, fmt.Errorf("fetching liked tracks: %w", err)
	}

	trackArtists, err := buildTrackArtistsMap(likedTracks, store)
	if err != nil {
		return runPlan{}, fmt.Errorf("building track-artist map: %w", err)
	}

	dedupResult, err := dedup.DeduplicateTracks(ctx, logger, store, likedTracks)
	if err != nil {
		return runPlan{}, fmt.Errorf("deduplicating tracks: %w", err)
	}

	desired := slices.Clone(dedupResult.RepresentativeTracks)
	slices.Reverse(desired) // liked tracks arrive newest first

	return runPlan{
		mode:                 ModeFull,
		desiredTracks:        desired,
		trackArtists:         trackArtists,
		duplicateIDs:         dedupResult.DuplicateIDs,
		likedTotal:           len(likedTracks),
		checkpoint:           cache.Checkpoint{LatestAddedAt: latestAddedAt(likedTracks)},
		refreshExpiredGenres: true,
	}, nil
}

// executePlan syncs the mirror and genre playlists towards plan, then saves
// the checkpoint and metrics.
func executePlan(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.Config,
	client *spotify.Client,
	store *cache.Store,
	opts RunOptions,
	plan runPlan,
	startedAt time.Time,
) (RunResult, error) {
	metrics := cache.RunMetrics{
		StartedAt:    startedAt,
		Mode:         string(plan.mode),
		LikedTotal:   plan.likedTotal,
		NewTracks:    plan.newTracks,
		DedupRemoved: len(plan.duplicateIDs),
		DuplicateIDs: spotifyIDsToStringSlice(plan.duplicateIDs),
	}

	stored, err := store.LoadCheckpoint()
	if err != nil {
		return RunResult{}, fmt.Errorf("loading checkpoint: %w", err)
	}

	mirrorResult, err := syncMirrorPlaylist(ctx, logger, cfg, client, store, plan.desiredTracks, opts.ForceRefresh)
	if err != nil {
		return RunResult{}, err
	}

	metrics.MirrorAdded = mirrorResult.Added
	metrics.MirrorRemoved = mirrorResult.Removed

	// Genre playlists are derived from the mirror, so they are due when it
	// changed. A genre sync that failed or was skipped on an earlier run is
	// remembered in the checkpoint, because the mirror will not change again.
	mirrorChanged := mirrorResult.Added+mirrorResult.Removed > 0
	genresDue := opts.EnableGenres && (mirrorChanged || opts.ForceRefresh || stored.GenresPending)
	pending := stored.GenresPending || mirrorChanged

	message := "sync run complete"
	genresSynced := false

	switch {
	case !opts.EnableGenres:
		message = "sync run complete (genres disabled)"
	case !genresDue:
		message = "sync run complete (genre sync skipped, no changes detected)"
	default:
		// Persist the pending flag first, so a failure or crash below leaves
		// the next run to retry the genre sync.
		if !stored.GenresPending {
			pendingCheckpoint := cache.Checkpoint{LatestAddedAt: stored.LatestAddedAt, GenresPending: true}
			if err = store.SaveCheckpoint(pendingCheckpoint); err != nil {
				return RunResult{}, fmt.Errorf("saving checkpoint: %w", err)
			}
		}

		counts, genreErr := syncGenrePlaylists(ctx, logger, cfg, client, store,
			plan.desiredTracks, plan.trackArtists, opts.ForceRefresh, plan.refreshExpiredGenres)
		if genreErr != nil {
			return RunResult{}, genreErr
		}

		metrics.BucketsCreated = counts.BucketsCreated
		metrics.BucketsRemoved = counts.BucketsRemoved
		genresSynced = true
		pending = false
	}

	final := cache.Checkpoint{LatestAddedAt: plan.checkpoint.LatestAddedAt, GenresPending: pending}
	if final.LatestAddedAt.IsZero() {
		final.LatestAddedAt = stored.LatestAddedAt
	}

	if err = store.SaveCheckpoint(final); err != nil {
		return RunResult{}, fmt.Errorf("saving checkpoint: %w", err)
	}

	return completeRun(ctx, logger, store, &metrics, message, genresSynced), nil
}

// latestAddedAt returns the most recent AddedAt among tracks, ignoring
// unparseable timestamps. Returns the zero time when there is none.
func latestAddedAt(tracks []spotifyio.LikedTrackData) time.Time {
	var latest time.Time
	for _, track := range tracks {
		if t, err := time.Parse(time.RFC3339, track.AddedAt); err == nil && t.After(latest) {
			latest = t
		}
	}
	return latest
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
	refreshExpired bool,
) (bucketCounts, error) {
	artistGenres, err := fetchArtistGenres(
		ctx,
		logger,
		cfg,
		client,
		store,
		trackIDs,
		trackArtists,
		refreshExpired,
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
	refreshExpired bool,
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
		refreshExpired,
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

// filterUnknownOrExpiredArtists returns artist IDs that are not in the cache
// or, when refreshExpired is set, whose entry is older than ttl.
func filterUnknownOrExpiredArtists(
	artistIDs []string,
	cached map[string]cache.ArtistGenreEntry,
	ttl time.Duration,
	refreshExpired bool,
) []string {
	now := time.Now()
	var result []string

	for _, artistID := range artistIDs {
		entry, ok := cached[artistID]
		if !ok {
			result = append(result, artistID)
			continue
		}

		if refreshExpired && now.Sub(entry.FetchedAt) > ttl {
			result = append(result, artistID)
		}
	}

	return result
}

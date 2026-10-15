// Package sync provides synchronization logic for mirror and genre playlists.
package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/omerzamir/spotify/v2"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/config"
	"spotify-syncer/internal/dedup"
	"spotify-syncer/internal/spotifyio"
)

// ReverseConfig holds parameters for importing songs from a playlist into Liked Songs.
type ReverseConfig struct {
	PlaylistIdentifier string
	BatchSize          int
	DryRun             bool
}

// ReverseResult holds the statistics and result of a reverse sync operation.
type ReverseResult struct {
	PlaylistName      string  `json:"playlist_name"`
	PlaylistID        string  `json:"playlist_id"`
	TotalTracks       int     `json:"total_tracks"`
	DuplicatesSkipped int     `json:"duplicates_skipped"`
	AlreadyLiked      int     `json:"already_liked"`
	NewlyAdded        int     `json:"newly_added"`
	DryRun            bool    `json:"dry_run"`
	DurationSec       float64 `json:"duration_sec"`
}

// Reverse syncs songs from a playlist (by name, ID, or URL) into the user's Liked Songs library.
// If PlaylistIdentifier is empty, it defaults to the configured mirror playlist name.
//
// Playlist tracks are deduplicated by ISRC first, so two releases of the same
// recording are liked once. A track counts as already liked when it, or any
// liked track sharing its ISRC, is in Liked Songs; such tracks are not re-saved,
// which avoids duplicate likes and preserves their original added-at timestamp.
func Reverse(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.Config,
	client *spotify.Client,
	store *cache.Store,
	reverseCfg ReverseConfig,
) (ReverseResult, error) {
	start := time.Now()

	targetIdentifier := reverseCfg.PlaylistIdentifier
	if targetIdentifier == "" {
		targetIdentifier = cfg.Sync.MirrorPlaylistName
	}

	logger.InfoContext(ctx, "resolving target playlist for reverse sync",
		"target", targetIdentifier)

	playlistID, playlistName, err := spotifyio.ResolvePlaylist(ctx, logger, client, targetIdentifier)
	if err != nil {
		return ReverseResult{}, fmt.Errorf("resolving playlist %q: %w", targetIdentifier, err)
	}

	logger.InfoContext(ctx, "fetching playlist tracks",
		"playlist_name", playlistName,
		"playlist_id", playlistID)

	playlistTracks, err := spotifyio.FetchPlaylistTracks(ctx, logger, client, playlistID)
	if err != nil {
		return ReverseResult{}, fmt.Errorf("fetching tracks from playlist %s: %w", playlistID, err)
	}

	candidates, duplicatesSkipped := dedup.UniqueTracks(playlistTrackRefs(playlistTracks))

	logger.InfoContext(ctx, "checking which tracks are already in Liked Songs",
		"total_unique_tracks", len(candidates),
		"duplicates_skipped", duplicatesSkipped)

	lookup, err := loadLikedLookup(ctx, logger, client, store)
	if err != nil {
		return ReverseResult{}, err
	}

	toAdd, alreadyLikedCount, err := partitionByLiked(ctx, logger, client, lookup, candidates)
	if err != nil {
		return ReverseResult{}, err
	}

	logger.InfoContext(ctx, "evaluated playlist tracks against Liked Songs",
		"playlist_name", playlistName,
		"total_tracks", len(candidates),
		"already_liked", alreadyLikedCount,
		"to_add", len(toAdd),
		"dry_run", reverseCfg.DryRun)

	if !reverseCfg.DryRun && len(toAdd) > 0 {
		if addErr := spotifyio.BatchAddTracksToLibrary(
			ctx,
			logger,
			client,
			toAdd,
			reverseCfg.BatchSize,
		); addErr != nil {
			return ReverseResult{}, fmt.Errorf("adding tracks to Liked Songs: %w", addErr)
		}
	}

	result := ReverseResult{
		PlaylistName:      playlistName,
		PlaylistID:        string(playlistID),
		TotalTracks:       len(candidates),
		DuplicatesSkipped: duplicatesSkipped,
		AlreadyLiked:      alreadyLikedCount,
		NewlyAdded:        len(toAdd),
		DryRun:            reverseCfg.DryRun,
		DurationSec:       time.Since(start).Seconds(),
	}

	if reverseCfg.DryRun {
		logger.InfoContext(ctx, "reverse sync dry run finished (no tracks were added)",
			"playlist_name", playlistName,
			"would_add", len(toAdd),
			"already_liked", alreadyLikedCount)
	} else {
		logger.InfoContext(ctx, "reverse sync finished successfully",
			"playlist_name", playlistName,
			"added", len(toAdd),
			"already_liked", alreadyLikedCount)
	}

	return result, nil
}

// likedLookup answers whether a track is already liked. Matches in fresh come
// from liked tracks fetched during this run and are trusted. Matches in cached
// come from the ISRC cache written by the last sync run; the user may have
// unliked those tracks since, so they must be confirmed against the library.
type likedLookup struct {
	fresh  *dedup.LikedIndex
	cached *dedup.LikedIndex
}

// loadLikedLookup builds a likedLookup as cheaply as possible. With sync state
// available (a checkpoint and the ISRC cache, which holds every liked track
// from the last full or fast run) it fetches only tracks liked since the
// checkpoint. Without it, it fetches the whole liked library.
func loadLikedLookup(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	store *cache.Store,
) (likedLookup, error) {
	lookup := likedLookup{
		fresh:  dedup.NewLikedIndex(),
		cached: dedup.NewLikedIndex(),
	}

	checkpoint, err := store.LoadCheckpoint()
	if err != nil {
		return likedLookup{}, fmt.Errorf("loading checkpoint: %w", err)
	}

	trackISRCs, err := store.LoadTrackISRC()
	if err != nil {
		return likedLookup{}, fmt.Errorf("loading ISRC cache: %w", err)
	}

	if checkpoint.LatestAddedAt.IsZero() || len(trackISRCs) == 0 {
		logger.InfoContext(ctx, "no sync state found, fetching full liked library for ISRC matching")

		liked, fetchErr := spotifyio.FetchAllLikedTracks(ctx, logger, client)
		if fetchErr != nil {
			return likedLookup{}, fmt.Errorf("fetching liked tracks: %w", fetchErr)
		}

		for _, track := range liked {
			lookup.fresh.Add(track.TrackID, track.ISRC)
		}

		return lookup, nil
	}

	for trackID, isrc := range trackISRCs {
		lookup.cached.Add(spotify.ID(trackID), isrc)
	}

	recent, err := spotifyio.FetchLikedTracksSince(ctx, logger, client, checkpoint.LatestAddedAt)
	if err != nil {
		return likedLookup{}, fmt.Errorf("fetching recently liked tracks: %w", err)
	}

	for _, track := range recent {
		lookup.fresh.Add(track.TrackID, track.ISRC)
	}

	logger.InfoContext(ctx, "built liked index from sync state",
		"cached_tracks", lookup.cached.Len(),
		"liked_since_checkpoint", lookup.fresh.Len())

	return lookup, nil
}

// partitionByLiked splits candidates into tracks to save and a count of tracks
// already liked, preserving candidate order. Cached matches are confirmed with
// a library check covering only the matched liked track IDs.
func partitionByLiked(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	lookup likedLookup,
	candidates []dedup.TrackRef,
) ([]spotify.ID, int, error) {
	var toVerify []spotify.ID
	seen := make(map[spotify.ID]struct{})

	for _, candidate := range candidates {
		if lookup.fresh.Matches(candidate) != nil {
			continue
		}
		for _, likedID := range lookup.cached.Matches(candidate) {
			if _, ok := seen[likedID]; !ok {
				seen[likedID] = struct{}{}
				toVerify = append(toVerify, likedID)
			}
		}
	}

	stillLiked, err := spotifyio.BatchCheckTracksInLibrary(ctx, logger, client, toVerify)
	if err != nil {
		return nil, 0, fmt.Errorf("checking liked tracks status: %w", err)
	}

	toAdd := make([]spotify.ID, 0, len(candidates))
	alreadyLiked := 0

	for _, candidate := range candidates {
		if isLiked(lookup, stillLiked, candidate) {
			alreadyLiked++
			continue
		}
		toAdd = append(toAdd, candidate.ID)
	}

	return toAdd, alreadyLiked, nil
}

// isLiked reports whether candidate matches a freshly fetched liked track, or a
// cached liked track that the library check confirmed is still liked.
func isLiked(lookup likedLookup, stillLiked map[spotify.ID]bool, candidate dedup.TrackRef) bool {
	if lookup.fresh.Matches(candidate) != nil {
		return true
	}

	for _, likedID := range lookup.cached.Matches(candidate) {
		if stillLiked[likedID] {
			return true
		}
	}

	return false
}

// playlistTrackRefs converts fetched playlist tracks into dedup references.
func playlistTrackRefs(tracks []spotifyio.PlaylistTrackData) []dedup.TrackRef {
	refs := make([]dedup.TrackRef, len(tracks))
	for i, track := range tracks {
		refs[i] = dedup.TrackRef{ID: track.TrackID, ISRC: track.ISRC}
	}
	return refs
}

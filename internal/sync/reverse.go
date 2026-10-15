// Package sync provides synchronization logic for mirror and genre playlists.
package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/zmb3/spotify/v2"

	"spotify-syncer/internal/config"
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
	PlaylistName string  `json:"playlist_name"`
	PlaylistID   string  `json:"playlist_id"`
	TotalTracks  int     `json:"total_tracks"`
	AlreadyLiked int     `json:"already_liked"`
	NewlyAdded   int     `json:"newly_added"`
	DryRun       bool    `json:"dry_run"`
	DurationSec  float64 `json:"duration_sec"`
}

// Reverse syncs songs from a playlist (by name, ID, or URL) into the user's Liked Songs library.
// If PlaylistIdentifier is empty, it defaults to the configured mirror playlist name.
// Tracks that are already liked are identified and not re-saved to avoid duplicate calls
// and to preserve their original added-at timestamp in Spotify.
func Reverse(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.Config,
	client *spotify.Client,
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

	trackIDs, err := spotifyio.FetchPlaylistTrackIDs(ctx, logger, client, playlistID)
	if err != nil {
		return ReverseResult{}, fmt.Errorf("fetching tracks from playlist %s: %w", playlistID, err)
	}

	uniqueTrackIDs := deduplicateTrackIDs(trackIDs)

	logger.InfoContext(ctx, "checking which tracks are already in Liked Songs",
		"total_unique_tracks", len(uniqueTrackIDs))

	hasMap, err := spotifyio.BatchCheckTracksInLibrary(ctx, logger, client, uniqueTrackIDs)
	if err != nil {
		return ReverseResult{}, fmt.Errorf("checking liked tracks status: %w", err)
	}

	toAdd := make([]spotify.ID, 0, len(uniqueTrackIDs))
	alreadyLikedCount := 0

	for _, id := range uniqueTrackIDs {
		if hasMap[id] {
			alreadyLikedCount++
		} else {
			toAdd = append(toAdd, id)
		}
	}

	logger.InfoContext(ctx, "evaluated playlist tracks against Liked Songs",
		"playlist_name", playlistName,
		"total_tracks", len(uniqueTrackIDs),
		"already_liked", alreadyLikedCount,
		"to_add", len(toAdd),
		"dry_run", reverseCfg.DryRun)

	if !reverseCfg.DryRun && len(toAdd) > 0 {
		if addErr := spotifyio.BatchAddTracksToLibrary(ctx, logger, client, toAdd, reverseCfg.BatchSize); addErr != nil {
			return ReverseResult{}, fmt.Errorf("adding tracks to Liked Songs: %w", addErr)
		}
	}

	result := ReverseResult{
		PlaylistName: playlistName,
		PlaylistID:   string(playlistID),
		TotalTracks:  len(uniqueTrackIDs),
		AlreadyLiked: alreadyLikedCount,
		NewlyAdded:   len(toAdd),
		DryRun:       reverseCfg.DryRun,
		DurationSec:  time.Since(start).Seconds(),
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

func deduplicateTrackIDs(ids []spotify.ID) []spotify.ID {
	seen := make(map[spotify.ID]bool, len(ids))
	unique := make([]spotify.ID, 0, len(ids))

	for _, id := range ids {
		if id == "" {
			continue
		}
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}

	return unique
}

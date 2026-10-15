// Package sync provides synchronization logic for mirror and genre playlists.
package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/zmb3/spotify/v2"

	"spotify-syncer/internal/config"
	"spotify-syncer/internal/spotifyio"
)

// MergeConfig holds parameters for merging two or more playlists into a new one.
type MergeConfig struct {
	Sources     []string
	TargetName  string
	Public      bool
	Deduplicate bool
	DryRun      bool
}

// SourcePlaylistInfo holds metadata about an individual source playlist in the merge.
type SourcePlaylistInfo struct {
	Name   string `json:"name"`
	ID     string `json:"id"`
	Tracks int    `json:"tracks"`
}

// MergeResult holds the statistics and result of a merge operation.
type MergeResult struct {
	Sources           []SourcePlaylistInfo `json:"sources"`
	TargetName        string               `json:"target_name"`
	TargetID          string               `json:"target_id,omitempty"`
	TotalTracks       int                  `json:"total_tracks"`
	UniqueTracks      int                  `json:"unique_tracks"`
	DuplicatesSkipped int                  `json:"duplicates_skipped"`
	DryRun            bool                 `json:"dry_run"`
	DurationSec       float64              `json:"duration_sec"`
}

// MinMergeSources is the minimum number of source playlists for a merge.
const MinMergeSources = 2

// MergePlaylists merges tracks from two or more source playlists into a newly created playlist.
// Source playlists can be identified by name, Spotify ID, or Spotify URL/URI.
func MergePlaylists(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.Config,
	client *spotify.Client,
	mergeCfg MergeConfig,
) (MergeResult, error) {
	start := time.Now()

	if len(mergeCfg.Sources) < MinMergeSources {
		return MergeResult{}, errors.New("at least two source playlists are required to merge")
	}

	logger.InfoContext(ctx, "resolving source playlists for merge",
		"count", len(mergeCfg.Sources))

	sourcesInfo := make([]SourcePlaylistInfo, 0, len(mergeCfg.Sources))
	sourceNames := make([]string, 0, len(mergeCfg.Sources))
	var allTrackIDs [][]spotify.ID
	totalRawTracks := 0

	for i, src := range mergeCfg.Sources {
		sID, sName, err := spotifyio.ResolvePlaylist(ctx, logger, client, src)
		if err != nil {
			return MergeResult{}, fmt.Errorf("resolving source playlist %d (%q): %w", i+1, src, err)
		}

		logger.InfoContext(ctx, "fetching tracks from source playlist",
			"index", i+1,
			"name", sName,
			"id", sID)

		tracks, err := spotifyio.FetchPlaylistTrackIDs(ctx, logger, client, sID)
		if err != nil {
			return MergeResult{}, fmt.Errorf("fetching tracks from source playlist %q (%s): %w", sName, sID, err)
		}

		sourcesInfo = append(sourcesInfo, SourcePlaylistInfo{
			Name:   sName,
			ID:     string(sID),
			Tracks: len(tracks),
		})
		sourceNames = append(sourceNames, fmt.Sprintf("%q", sName))
		allTrackIDs = append(allTrackIDs, tracks)
		totalRawTracks += len(tracks)
	}

	var combined []spotify.ID
	duplicatesSkipped := 0

	if mergeCfg.Deduplicate {
		seen := make(map[spotify.ID]bool, totalRawTracks)
		combined = make([]spotify.ID, 0, totalRawTracks)

		for _, trackList := range allTrackIDs {
			for _, id := range trackList {
				if id == "" {
					continue
				}
				if !seen[id] {
					seen[id] = true
					combined = append(combined, id)
				}
			}
		}

		duplicatesSkipped = totalRawTracks - len(combined)
	} else {
		combined = make([]spotify.ID, 0, totalRawTracks)
		for _, trackList := range allTrackIDs {
			for _, id := range trackList {
				if id != "" {
					combined = append(combined, id)
				}
			}
		}
	}

	logger.InfoContext(ctx, "prepared combined track list for merged playlist",
		"source_count", len(mergeCfg.Sources),
		"total_raw_tracks", totalRawTracks,
		"combined_tracks", len(combined),
		"duplicates_skipped", duplicatesSkipped,
		"dry_run", mergeCfg.DryRun)

	var targetPlaylistID spotify.ID

	if !mergeCfg.DryRun {
		user, userErr := client.CurrentUser(ctx)
		if userErr != nil {
			return MergeResult{}, fmt.Errorf("getting current user: %w", userErr)
		}

		desc := fmt.Sprintf("Merged from %d playlists (%s) by Spotify Syncer", len(mergeCfg.Sources), strings.Join(sourceNames, ", "))
		newPlaylist, createErr := client.CreatePlaylistForUser(ctx, user.ID, mergeCfg.TargetName, desc, mergeCfg.Public, false)
		if createErr != nil {
			return MergeResult{}, fmt.Errorf("creating target playlist %q: %w", mergeCfg.TargetName, createErr)
		}

		targetPlaylistID = newPlaylist.ID
		logger.InfoContext(ctx, "created new playlist for merge",
			"name", mergeCfg.TargetName,
			"playlist_id", targetPlaylistID)

		batchSize := cfg.Sync.BatchSize
		if batchSize <= 0 {
			batchSize = 100
		}

		if addErr := spotifyio.BatchAddTracks(ctx, logger, client, targetPlaylistID, combined, batchSize); addErr != nil {
			return MergeResult{}, fmt.Errorf("adding tracks to merged playlist: %w", addErr)
		}
	}

	result := MergeResult{
		Sources:           sourcesInfo,
		TargetName:        mergeCfg.TargetName,
		TargetID:          string(targetPlaylistID),
		TotalTracks:       totalRawTracks,
		UniqueTracks:      len(combined),
		DuplicatesSkipped: duplicatesSkipped,
		DryRun:            mergeCfg.DryRun,
		DurationSec:       time.Since(start).Seconds(),
	}

	if mergeCfg.DryRun {
		logger.InfoContext(ctx, "merge dry run complete (no playlist created)",
			"target_name", mergeCfg.TargetName,
			"unique_tracks", len(combined))
	} else {
		logger.InfoContext(ctx, "playlist merge complete",
			"target_name", mergeCfg.TargetName,
			"target_id", targetPlaylistID,
			"added_tracks", len(combined))
	}

	return result, nil
}

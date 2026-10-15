// Package sync provides synchronization logic for mirror and genre playlists.
package sync

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/omerzamir/spotify/v2"

	"spotify-syncer/internal/config"
	"spotify-syncer/internal/dedup"
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
		return MergeResult{}, fmt.Errorf("at least %d source playlists are required to merge", MinMergeSources)
	}

	sources, allTracks, err := fetchMergeSources(ctx, logger, client, mergeCfg.Sources)
	if err != nil {
		return MergeResult{}, err
	}

	totalRawTracks := 0
	for _, source := range sources {
		totalRawTracks += source.Tracks
	}

	combined, duplicatesSkipped := combineSources(allTracks, mergeCfg.Deduplicate)

	logger.InfoContext(ctx, "prepared combined track list for merged playlist",
		"source_count", len(sources),
		"total_raw_tracks", totalRawTracks,
		"combined_tracks", len(combined),
		"duplicates_skipped", duplicatesSkipped,
		"dry_run", mergeCfg.DryRun)

	result := MergeResult{
		Sources:           sources,
		TargetName:        mergeCfg.TargetName,
		TotalTracks:       totalRawTracks,
		UniqueTracks:      len(combined),
		DuplicatesSkipped: duplicatesSkipped,
		DryRun:            mergeCfg.DryRun,
	}

	if mergeCfg.DryRun {
		logger.InfoContext(ctx, "merge dry run complete (no playlist created)",
			"target_name", mergeCfg.TargetName,
			"unique_tracks", len(combined))

		result.DurationSec = time.Since(start).Seconds()

		return result, nil
	}

	targetID, err := createMergedPlaylist(ctx, logger, cfg, client, mergeCfg, sources, combined)
	if err != nil {
		return MergeResult{}, err
	}

	result.TargetID = string(targetID)
	result.DurationSec = time.Since(start).Seconds()

	logger.InfoContext(ctx, "playlist merge complete",
		"target_name", mergeCfg.TargetName,
		"target_id", targetID,
		"added_tracks", len(combined))

	return result, nil
}

// fetchMergeSources resolves each source playlist and fetches its tracks.
func fetchMergeSources(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	identifiers []string,
) ([]SourcePlaylistInfo, [][]dedup.TrackRef, error) {
	logger.InfoContext(ctx, "resolving source playlists for merge",
		"count", len(identifiers))

	sources := make([]SourcePlaylistInfo, 0, len(identifiers))
	allTracks := make([][]dedup.TrackRef, 0, len(identifiers))

	for i, identifier := range identifiers {
		id, name, err := spotifyio.ResolvePlaylist(ctx, logger, client, identifier)
		if err != nil {
			return nil, nil, fmt.Errorf("resolving source playlist %d (%q): %w", i+1, identifier, err)
		}

		logger.InfoContext(ctx, "fetching tracks from source playlist",
			"index", i+1,
			"name", name,
			"id", id)

		tracks, err := spotifyio.FetchPlaylistTracks(ctx, logger, client, id)
		if err != nil {
			return nil, nil, fmt.Errorf("fetching tracks from source playlist %q (%s): %w", name, id, err)
		}

		sources = append(sources, SourcePlaylistInfo{Name: name, ID: string(id), Tracks: len(tracks)})
		allTracks = append(allTracks, playlistTrackRefs(tracks))
	}

	return sources, allTracks, nil
}

// createMergedPlaylist creates the target playlist and fills it with tracks.
func createMergedPlaylist(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.Config,
	client *spotify.Client,
	mergeCfg MergeConfig,
	sources []SourcePlaylistInfo,
	tracks []spotify.ID,
) (spotify.ID, error) {
	quotedNames := make([]string, len(sources))
	for i, source := range sources {
		quotedNames[i] = strconv.Quote(source.Name)
	}

	description := fmt.Sprintf("Merged from %d playlists (%s) by Spotify Syncer",
		len(sources), strings.Join(quotedNames, ", "))

	targetID, err := spotifyio.CreatePlaylist(ctx, logger, client, mergeCfg.TargetName, description, mergeCfg.Public)
	if err != nil {
		return "", fmt.Errorf("creating target playlist %q: %w", mergeCfg.TargetName, err)
	}

	if err = spotifyio.BatchAddTracks(ctx, logger, client, targetID, tracks, cfg.Sync.BatchSize); err != nil {
		return "", fmt.Errorf("adding tracks to merged playlist: %w", err)
	}

	return targetID, nil
}

// combineSources concatenates the source playlists' tracks in order. With
// deduplicate set, a track is dropped when an earlier track shares its ID or
// ISRC (the same recording released as a single and on an album). Returns the
// combined track IDs and the number of duplicates dropped.
func combineSources(sources [][]dedup.TrackRef, deduplicate bool) ([]spotify.ID, int) {
	var all []dedup.TrackRef
	for _, tracks := range sources {
		all = append(all, tracks...)
	}

	if !deduplicate {
		combined := make([]spotify.ID, 0, len(all))
		for _, track := range all {
			if track.ID != "" {
				combined = append(combined, track.ID)
			}
		}
		return combined, 0
	}

	unique, skipped := dedup.UniqueTracks(all)
	return dedup.TrackIDs(unique), skipped
}

// Package dedup provides ISRC-based track deduplication for Spotify tracks.
package dedup

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/zmb3/spotify/v2"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/spotifyio"
)

// DeduplicationResult holds the result of deduplicating liked tracks.
type DeduplicationResult struct {
	RepresentativeTracks []spotify.ID
	DuplicateIDs         []spotify.ID
	ISRCMap              map[string]string
}

// DeduplicateTracks groups liked tracks by ISRC code and selects a single
// representative track for each ISRC group. Tracks without ISRC codes are
// always kept. The selection prefers playable tracks, then most recently
// added tracks. Representatives and duplicates keep the order of tracks.
// Replaces the ISRC cache with all track-to-ISRC mappings, so the cache lists
// every liked track.
func DeduplicateTracks(
	ctx context.Context,
	logger *slog.Logger,
	store *cache.Store,
	tracks []spotifyio.LikedTrackData,
) (DeduplicationResult, error) {
	isrcMap := make(map[string]string, len(tracks))
	isrcGroups := make(map[string][]spotifyio.LikedTrackData)

	for _, track := range tracks {
		isrcMap[string(track.TrackID)] = track.ISRC

		if track.ISRC != "" {
			isrcGroups[track.ISRC] = append(isrcGroups[track.ISRC], track)
		}
	}

	if err := store.SaveTrackISRC(isrcMap); err != nil {
		return DeduplicationResult{}, fmt.Errorf("saving ISRC cache: %w", err)
	}

	representatives := make(map[string]spotify.ID, len(isrcGroups))
	for isrc, group := range isrcGroups {
		representative := selectRepresentative(group)
		representatives[isrc] = representative.TrackID

		if len(group) > 1 {
			logger.DebugContext(ctx, "deduplicated ISRC group",
				"isrc", isrc,
				"total_tracks", len(group),
				"representative", representative.TrackID,
				"duplicates", len(group)-1)
		}
	}

	var representativeTracks, duplicateIDs []spotify.ID

	for _, track := range tracks {
		if track.ISRC == "" || representatives[track.ISRC] == track.TrackID {
			representativeTracks = append(representativeTracks, track.TrackID)
		} else {
			duplicateIDs = append(duplicateIDs, track.TrackID)
		}
	}

	logger.InfoContext(ctx, "deduplication complete",
		"total_tracks", len(tracks),
		"representative_tracks", len(representativeTracks),
		"duplicate_tracks", len(duplicateIDs))

	return DeduplicationResult{
		RepresentativeTracks: representativeTracks,
		DuplicateIDs:         duplicateIDs,
		ISRCMap:              isrcMap,
	}, nil
}

// selectRepresentative picks the best representative track from a group of
// tracks with the same ISRC. Selection criteria (in order):
// 1. Prefer playable tracks over non-playable
// 2. Prefer most recently added (latest AddedAt)
// 3. Prefer the lowest track ID, so the choice is deterministic.
func selectRepresentative(group []spotifyio.LikedTrackData) spotifyio.LikedTrackData {
	return slices.MinFunc(group, compareRepresentatives)
}

// compareRepresentatives orders a before b when a is the better representative.
func compareRepresentatives(a, b spotifyio.LikedTrackData) int {
	if a.IsPlayable != b.IsPlayable {
		if a.IsPlayable {
			return -1
		}
		return 1
	}

	timeA, errA := parseAddedAt(a.AddedAt)
	timeB, errB := parseAddedAt(b.AddedAt)

	switch {
	case errA == nil && errB != nil:
		return -1
	case errA != nil && errB == nil:
		return 1
	case errA == nil && errB == nil:
		if c := timeB.Compare(timeA); c != 0 {
			return c // newer first
		}
	}

	return cmp.Compare(a.TrackID, b.TrackID)
}

// parseAddedAt parses the AddedAt timestamp string from Spotify's API.
// The format is RFC3339: "2006-01-02T15:04:05Z".
func parseAddedAt(addedAt string) (time.Time, error) {
	return time.Parse(time.RFC3339, addedAt)
}

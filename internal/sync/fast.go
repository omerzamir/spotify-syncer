package sync

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/omerzamir/spotify/v2"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/set"
	"spotify-syncer/internal/spotifyio"
)

// planFastRun builds a plan that appends tracks liked since the checkpoint to
// the cached mirror contents. It reports ok=false, without error, when fast
// mode cannot run and a full run is needed instead: on the first run, after
// the mirror cache was evicted, or when forceRefresh is set.
//
// Fast runs keep the ISRC cache complete (it lists every liked track seen so
// far) so later fast and reverse runs can match by ISRC without fetching the
// whole library.
func planFastRun(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	store *cache.Store,
	forceRefresh bool,
) (runPlan, bool, error) {
	if forceRefresh {
		logger.InfoContext(ctx, "force refresh requested, running full sync instead of fast")
		return runPlan{}, false, nil
	}

	checkpoint, err := store.LoadCheckpoint()
	if err != nil {
		return runPlan{}, false, fmt.Errorf("loading checkpoint: %w", err)
	}

	mirrorTracks, err := store.LoadMirrorTracks()
	if err != nil {
		return runPlan{}, false, fmt.Errorf("loading mirror tracks cache: %w", err)
	}

	trackISRCs, err := store.LoadTrackISRC()
	if err != nil {
		return runPlan{}, false, fmt.Errorf("loading ISRC cache: %w", err)
	}

	if checkpoint.LatestAddedAt.IsZero() || len(mirrorTracks) == 0 || len(trackISRCs) == 0 {
		logger.InfoContext(ctx, "no fast-mode sync state yet, running full sync",
			"has_checkpoint", !checkpoint.LatestAddedAt.IsZero(),
			"cached_mirror_tracks", len(mirrorTracks),
			"cached_isrcs", len(trackISRCs))
		return runPlan{}, false, nil
	}

	newTracks, err := spotifyio.FetchLikedTracksSince(ctx, logger, client, checkpoint.LatestAddedAt)
	if err != nil {
		return runPlan{}, false, fmt.Errorf("fetching tracks liked since checkpoint: %w", err)
	}

	mirror := stringSliceToSpotifyIDs(mirrorTracks)
	added, duplicates := appendNewTracks(mirror, trackISRCs, newTracks)

	unseen := 0
	for _, track := range newTracks {
		if _, known := trackISRCs[string(track.TrackID)]; !known {
			unseen++
		}
		trackISRCs[string(track.TrackID)] = track.ISRC
	}

	if err = store.SaveTrackISRC(trackISRCs); err != nil {
		return runPlan{}, false, fmt.Errorf("saving ISRC cache: %w", err)
	}

	trackArtists, err := buildTrackArtistsMap(newTracks, store)
	if err != nil {
		return runPlan{}, false, fmt.Errorf("building track-artist map: %w", err)
	}

	latest := checkpoint.LatestAddedAt
	if newest := latestAddedAt(newTracks); newest.After(latest) {
		latest = newest
	}

	logger.InfoContext(ctx, "planned fast sync",
		"checkpoint", checkpoint.LatestAddedAt,
		"fetched_since_checkpoint", len(newTracks),
		"new_liked_tracks", unseen,
		"to_append", len(added),
		"isrc_duplicates", len(duplicates))

	return runPlan{
		mode:          ModeFast,
		desiredTracks: append(mirror, added...),
		trackArtists:  trackArtists,
		duplicateIDs:  duplicates,
		likedTotal:    len(trackISRCs),
		newTracks:     unseen,
		checkpoint:    cache.Checkpoint{LatestAddedAt: latest},
	}, true, nil
}

// appendNewTracks decides which newly liked tracks (newest first, as fetched)
// to append to the mirror, oldest first. A track is skipped when it is
// already in the mirror, and reported as a duplicate when its ISRC matches a
// mirror track (looked up in knownISRCs) or an earlier new track. Unlike a
// full run, an existing mirror track is never swapped for a better release
// of the same recording.
func appendNewTracks(
	mirror []spotify.ID,
	knownISRCs map[string]string,
	newTracks []spotifyio.LikedTrackData,
) ([]spotify.ID, []spotify.ID) {
	inMirror := set.Of(mirror...)
	seenISRCs := set.New[string](len(mirror))

	for _, id := range mirror {
		if isrc := knownISRCs[string(id)]; isrc != "" {
			seenISRCs.Add(isrc)
		}
	}

	var added, duplicates []spotify.ID

	for _, track := range slices.Backward(newTracks) {
		if inMirror.Contains(track.TrackID) {
			continue
		}

		if track.ISRC != "" && !seenISRCs.Add(track.ISRC) {
			duplicates = append(duplicates, track.TrackID)
			continue
		}

		inMirror.Add(track.TrackID)
		added = append(added, track.TrackID)
	}

	return added, duplicates
}

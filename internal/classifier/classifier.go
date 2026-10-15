// Package classifier provides dynamic genre bucket discovery and track
// classification based on artist genre tags.
package classifier

import (
	"context"
	"log/slog"
	"maps"
	"slices"

	"github.com/zmb3/spotify/v2"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/config"
	"spotify-syncer/internal/set"
)

// ClassifyResult holds the result of classifying tracks into genre buckets.
type ClassifyResult struct {
	Buckets map[string][]spotify.ID
}

// Classify assigns deduplicated tracks to genre buckets using a greedy
// frequency-based algorithm. For each track, it unions all artists' top N
// genre tags. Then it selects the most frequent genres as buckets, assigning
// each track to exactly one bucket. Tracks without matching genres are left
// unplaced (no "Other" bucket).
func Classify(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.GenreConfig,
	trackIDs []spotify.ID,
	trackArtists map[string][]string,
	artistGenres map[string]cache.ArtistGenreEntry,
) (ClassifyResult, error) {
	trackGenreMap := buildTrackGenreMap(
		ctx,
		logger,
		cfg.GenresPerArtist,
		trackIDs,
		trackArtists,
		artistGenres,
	)

	genreFrequency := buildGenreFrequency(trackIDs, trackGenreMap)

	buckets := greedyBucketSelection(
		ctx,
		logger,
		cfg,
		genreFrequency,
	)

	logger.InfoContext(ctx, "classification complete",
		"total_tracks", len(trackIDs),
		"buckets_created", len(buckets),
		"tracks_assigned", countAssignedTracks(buckets),
		"tracks_unplaced", len(trackIDs)-countAssignedTracks(buckets))

	return ClassifyResult{Buckets: buckets}, nil
}

// buildTrackGenreMap creates a map from track ID to its combined genre set.
// For multi-artist tracks, it unions the top N genres from each artist.
func buildTrackGenreMap(
	ctx context.Context,
	logger *slog.Logger,
	genresPerArtist int,
	trackIDs []spotify.ID,
	trackArtists map[string][]string,
	artistGenres map[string]cache.ArtistGenreEntry,
) map[spotify.ID]set.Set[string] {
	result := make(map[spotify.ID]set.Set[string])

	for _, trackID := range trackIDs {
		artistIDs, ok := trackArtists[string(trackID)]
		if !ok || len(artistIDs) == 0 {
			logger.DebugContext(ctx, "track has no artists, skipping genre assignment",
				"track_id", trackID)
			continue
		}

		combinedGenres := set.New[string](0)

		for _, artistID := range artistIDs {
			entry, found := artistGenres[artistID]
			if !found {
				continue
			}

			for _, genre := range entry.Genres[:min(len(entry.Genres), genresPerArtist)] {
				combinedGenres.Add(genre)
			}
		}

		if combinedGenres.Len() > 0 {
			result[trackID] = combinedGenres
		}
	}

	return result
}

// buildGenreFrequency maps each genre tag to the tracks that have it (via any
// of their artists), in the order the tracks appear in trackIDs.
func buildGenreFrequency(
	trackIDs []spotify.ID,
	trackGenreMap map[spotify.ID]set.Set[string],
) map[string][]spotify.ID {
	frequency := make(map[string][]spotify.ID)

	for _, trackID := range trackIDs {
		for genre := range trackGenreMap[trackID].All() {
			frequency[genre] = append(frequency[genre], trackID)
		}
	}

	return frequency
}

// greedyBucketSelection implements the greedy frequency-based bucket selection
// algorithm. It repeatedly picks the genre with the most unassigned tracks and
// assigns those tracks to it, so each track lands in at most one bucket.
// Selection is deterministic: ties go to the alphabetically first genre, and
// bucket tracks keep their input order.
func greedyBucketSelection(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.GenreConfig,
	genreFrequency map[string][]spotify.ID,
) map[string][]spotify.ID {
	buckets := make(map[string][]spotify.ID)
	assignedTracks := set.New[spotify.ID](0)

	for len(buckets) < cfg.MaxBuckets {
		bestGenre, tracksToAssign := findBestGenre(genreFrequency, assignedTracks, cfg.MinBucketSize)
		if bestGenre == "" {
			break
		}

		buckets[bestGenre] = tracksToAssign
		delete(genreFrequency, bestGenre)

		for _, trackID := range tracksToAssign {
			assignedTracks.Add(trackID)
		}

		logger.InfoContext(ctx, "created genre bucket",
			"genre", bestGenre,
			"tracks", len(tracksToAssign))
	}

	return buckets
}

// findBestGenre returns the genre with the most unassigned tracks that meets
// the minimum bucket size, along with those tracks. Ties go to the
// alphabetically first genre. Returns "" if no genre qualifies.
func findBestGenre(
	genreFrequency map[string][]spotify.ID,
	assignedTracks set.Set[spotify.ID],
	minBucketSize int,
) (string, []spotify.ID) {
	var (
		bestGenre  string
		bestTracks []spotify.ID
	)

	for _, genre := range slices.Sorted(maps.Keys(genreFrequency)) {
		unassigned := set.Missing(genreFrequency[genre], assignedTracks)
		if len(unassigned) >= minBucketSize && len(unassigned) > len(bestTracks) {
			bestGenre, bestTracks = genre, unassigned
		}
	}

	return bestGenre, bestTracks
}

// countAssignedTracks returns the total number of tracks assigned across all buckets.
func countAssignedTracks(buckets map[string][]spotify.ID) int {
	total := 0
	for _, tracks := range buckets {
		total += len(tracks)
	}
	return total
}

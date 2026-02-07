// Package classifier provides dynamic genre bucket discovery and track
// classification based on artist genre tags.
package classifier

import (
	"context"
	"log/slog"
	"sort"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/zmb3/spotify/v2"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/config"
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

	genreFrequency := buildGenreFrequency(trackGenreMap)

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
) map[spotify.ID]mapset.Set[string] {
	result := make(map[spotify.ID]mapset.Set[string])

	for _, trackID := range trackIDs {
		trackIDStr := string(trackID)
		artistIDs, ok := trackArtists[trackIDStr]
		if !ok || len(artistIDs) == 0 {
			logger.DebugContext(ctx, "track has no artists, skipping genre assignment",
				"track_id", trackID)
			continue
		}

		combinedGenres := mapset.NewSet[string]()

		for _, artistID := range artistIDs {
			entry, found := artistGenres[artistID]
			if !found || len(entry.Genres) == 0 {
				continue
			}

			topN := genresPerArtist
			if len(entry.Genres) < topN {
				topN = len(entry.Genres)
			}

			for i := range topN {
				combinedGenres.Add(entry.Genres[i])
			}
		}

		if combinedGenres.Cardinality() > 0 {
			result[trackID] = combinedGenres
		}
	}

	return result
}

// buildGenreFrequency creates a frequency map from genre tag to the set of
// track IDs that have that genre (via any of their artists).
func buildGenreFrequency(
	trackGenreMap map[spotify.ID]mapset.Set[string],
) map[string]mapset.Set[spotify.ID] {
	frequency := make(map[string]mapset.Set[spotify.ID])

	for trackID, genres := range trackGenreMap {
		for genre := range genres.Iter() {
			if frequency[genre] == nil {
				frequency[genre] = mapset.NewSet[spotify.ID]()
			}
			frequency[genre].Add(trackID)
		}
	}

	return frequency
}

// greedyBucketSelection implements the greedy frequency-based bucket selection
// algorithm. It picks genres with the most unassigned tracks, creates buckets,
// assigns tracks, and removes assigned tracks from other genre counts.
func greedyBucketSelection(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.GenreConfig,
	genreFrequency map[string]mapset.Set[spotify.ID],
) map[string][]spotify.ID {
	buckets := make(map[string][]spotify.ID)
	assignedTracks := mapset.NewSet[spotify.ID]()

	for len(buckets) < cfg.MaxBuckets {
		bestGenre := findBestGenre(genreFrequency, assignedTracks, cfg.MinBucketSize)
		if bestGenre == "" {
			break
		}

		tracksToAssign := genreFrequency[bestGenre].Difference(assignedTracks).ToSlice()
		buckets[bestGenre] = tracksToAssign

		for _, trackID := range tracksToAssign {
			assignedTracks.Add(trackID)
		}

		logger.InfoContext(ctx, "created genre bucket",
			"genre", bestGenre,
			"tracks", len(tracksToAssign))

		removeAssignedTracks(genreFrequency, assignedTracks)
	}

	return buckets
}

// findBestGenre finds the genre with the most unassigned tracks that meets
// the minimum bucket size threshold. Returns empty string if no genre qualifies.
func findBestGenre(
	genreFrequency map[string]mapset.Set[spotify.ID],
	assignedTracks mapset.Set[spotify.ID],
	minBucketSize int,
) string {
	type genreCount struct {
		name  string
		count int
	}

	var candidates []genreCount

	for genre, tracks := range genreFrequency {
		unassigned := tracks.Difference(assignedTracks)
		count := unassigned.Cardinality()
		if count >= minBucketSize {
			candidates = append(candidates, genreCount{
				name:  genre,
				count: count,
			})
		}
	}

	if len(candidates) == 0 {
		return ""
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].count > candidates[j].count
	})

	return candidates[0].name
}

// removeAssignedTracks removes assigned tracks from all genre frequency counts.
// This ensures that once a track is assigned to a bucket, it doesn't appear
// in the unassigned count for other genres.
func removeAssignedTracks(
	genreFrequency map[string]mapset.Set[spotify.ID],
	assignedTracks mapset.Set[spotify.ID],
) {
	for genre := range genreFrequency {
		genreFrequency[genre] = genreFrequency[genre].Difference(assignedTracks)
	}
}

// countAssignedTracks returns the total number of tracks assigned across all buckets.
func countAssignedTracks(buckets map[string][]spotify.ID) int {
	total := 0
	for _, tracks := range buckets {
		total += len(tracks)
	}
	return total
}

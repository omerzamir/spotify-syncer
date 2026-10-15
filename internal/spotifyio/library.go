// Package spotifyio provides Spotify API client wrappers with rate limiting,
// retry logic, and optimized paging helpers.
package spotifyio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/zmb3/spotify/v2"
)

const (
	maxLibraryBatchSize = 50
)

// ExtractPlaylistID inspects an input string and extracts a Spotify playlist ID if
// the input is formatted as a Spotify URI, an open.spotify.com URL, or a direct
// 22-character Spotify ID. Returns the extracted ID and true if identified, or
// empty string and false if the input appears to be a plain playlist name.
func ExtractPlaylistID(input string) (string, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", false
	}

	if strings.HasPrefix(input, "spotify:playlist:") {
		id := strings.TrimPrefix(input, "spotify:playlist:")
		if id != "" {
			return id, true
		}
	}

	if strings.Contains(input, "open.spotify.com/playlist/") {
		parts := strings.Split(input, "open.spotify.com/playlist/")
		if len(parts) > 1 {
			idPart := parts[1]
			if qIdx := strings.Index(idPart, "?"); qIdx != -1 {
				idPart = idPart[:qIdx]
			}
			idPart = strings.Trim(idPart, "/")
			if idPart != "" {
				return idPart, true
			}
		}
	}

	if len(input) == 22 && isAlphanumeric(input) {
		return input, true
	}

	return "", false
}

func isAlphanumeric(s string) bool {
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// ResolvePlaylist resolves a playlist identifier (URL, URI, 22-character ID,
// or playlist name) to a Spotify ID and its display name.
func ResolvePlaylist(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	input string,
) (spotify.ID, string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", errors.New("playlist identifier cannot be empty")
	}

	if idStr, ok := ExtractPlaylistID(input); ok {
		playlistID := spotify.ID(idStr)
		playlist, err := client.GetPlaylist(ctx, playlistID)
		if err == nil {
			return playlistID, playlist.Name, nil
		}
		logger.DebugContext(ctx, "could not fetch playlist by direct ID, falling back to name search",
			"id", idStr, "error", err)
	}

	playlistID, found, err := findPlaylistByName(ctx, client, input)
	if err != nil {
		return "", "", fmt.Errorf("searching for playlist %q: %w", input, err)
	}
	if found {
		return playlistID, input, nil
	}

	return "", "", fmt.Errorf("playlist %q not found (tried as ID/URL and user playlist name)", input)
}

// BatchAddTracksToLibrary saves tracks to the current user's Liked Songs library
// in batches of up to 50 tracks (Spotify API maximum).
func BatchAddTracksToLibrary(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	trackIDs []spotify.ID,
	batchSize int,
) error {
	if len(trackIDs) == 0 {
		return nil
	}

	if batchSize <= 0 || batchSize > maxLibraryBatchSize {
		batchSize = maxLibraryBatchSize
	}

	for i := 0; i < len(trackIDs); i += batchSize {
		end := min(i+batchSize, len(trackIDs))
		batch := trackIDs[i:end]

		if err := client.AddTracksToLibrary(ctx, batch...); err != nil {
			return fmt.Errorf("saving tracks batch %d-%d to library: %w (ensure 'syncer auth' has been run to grant 'user-library-modify' permission)", i, end-1, err)
		}

		logger.DebugContext(ctx, "saved tracks batch to library",
			"batch_start", i,
			"batch_end", end-1,
			"batch_size", len(batch))
	}

	logger.InfoContext(ctx, "saved all tracks to library",
		"total_tracks", len(trackIDs))

	return nil
}

// BatchCheckTracksInLibrary checks which tracks are already saved in the current
// user's Liked Songs library. Queries in batches of up to 50 tracks.
// Returns a map where key is track ID and value is true if already saved.
func BatchCheckTracksInLibrary(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	trackIDs []spotify.ID,
) (map[spotify.ID]bool, error) {
	result := make(map[spotify.ID]bool, len(trackIDs))
	if len(trackIDs) == 0 {
		return result, nil
	}

	for i := 0; i < len(trackIDs); i += maxLibraryBatchSize {
		end := min(i+maxLibraryBatchSize, len(trackIDs))
		batch := trackIDs[i:end]

		contains, err := client.UserHasTracks(ctx, batch...)
		if err != nil {
			return nil, fmt.Errorf("checking tracks library status batch %d-%d: %w", i, end-1, err)
		}

		for j, hasTrack := range contains {
			result[batch[j]] = hasTrack
		}

		logger.DebugContext(ctx, "checked tracks library status batch",
			"batch_start", i,
			"batch_end", end-1,
			"batch_size", len(batch))
	}

	return result, nil
}

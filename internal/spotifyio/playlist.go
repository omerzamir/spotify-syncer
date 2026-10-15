// Package spotifyio provides Spotify API client wrappers with rate limiting,
// retry logic, and optimized paging helpers.
package spotifyio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/omerzamir/spotify/v2"

	"spotify-syncer/internal/cache"
)

const (
	playlistSearchPageSize = 50
)

// FindOrCreatePlaylist searches for a playlist by exact name (optionally with
// a prefix). If found, returns its ID. If not found, creates a new playlist
// with the given name and returns its ID. The search is case-sensitive and
// exact-match only. Uses pagination to search through all user playlists.
func FindOrCreatePlaylist(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	name string,
	prefix string,
	public bool,
) (spotify.ID, error) {
	fullName := name
	if prefix != "" {
		fullName = prefix + name
	}

	playlistID, found, err := findPlaylistByName(ctx, client, fullName)
	if err != nil {
		return "", fmt.Errorf("searching for playlist %q: %w", fullName, err)
	}

	if found {
		logger.InfoContext(ctx, "found existing playlist",
			"name", fullName,
			"playlist_id", playlistID)
		return playlistID, nil
	}

	return CreatePlaylist(ctx, logger, client, fullName, "", public)
}

// CreatePlaylist creates a new, non-collaborative playlist owned by the
// current user and returns its ID. It uses POST /me/playlists and falls back
// to the legacy POST /users/{id}/playlists.
func CreatePlaylist(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	name string,
	description string,
	public bool,
) (spotify.ID, error) {
	var playlist *spotify.FullPlaylist

	err := withLegacyFallback(ctx, logger, "POST /me/playlists",
		func() error {
			var err error
			playlist, err = client.CreatePlaylist(ctx, name, description, public, false)
			return err
		},
		func() error {
			user, err := client.CurrentUser(ctx)
			if err != nil {
				return fmt.Errorf("getting current user: %w", err)
			}

			//nolint:staticcheck // SA1019: the legacy endpoint is the deliberate fallback.
			playlist, err = client.CreatePlaylistForUser(ctx, user.ID, name, description, public, false)
			return err
		},
	)
	if err != nil {
		return "", fmt.Errorf("creating playlist %q: %w", name, err)
	}

	logger.InfoContext(ctx, "created new playlist",
		"name", name,
		"playlist_id", playlist.ID)

	return playlist.ID, nil
}

// findPlaylistByName searches through all user playlists for an exact name match.
// Returns the playlist ID and whether it was found.
func findPlaylistByName(
	ctx context.Context,
	client *spotify.Client,
	name string,
) (spotify.ID, bool, error) {
	page, err := client.CurrentUsersPlaylists(
		ctx,
		spotify.Limit(playlistSearchPageSize),
	)
	if err != nil {
		return "", false, fmt.Errorf("fetching first page of playlists: %w", err)
	}

	for {
		for _, playlist := range page.Playlists {
			if playlist.Name == name {
				return playlist.ID, true, nil
			}
		}

		err = client.NextPage(ctx, page)
		if errors.Is(err, spotify.ErrNoMorePages) {
			break
		}
		if err != nil {
			return "", false, fmt.Errorf("paging playlists: %w", err)
		}
	}

	return "", false, nil
}

// batchOperation performs a batch operation on tracks in a playlist.
type batchOperation func(ctx context.Context, playlistID spotify.ID, batch []spotify.ID) error

// performBatchOperation executes a batch operation on tracks, splitting them
// into batches of the specified size.
func performBatchOperation(
	ctx context.Context,
	logger *slog.Logger,
	playlistID spotify.ID,
	trackIDs []spotify.ID,
	batchSize int,
	op batchOperation,
	operationName string,
	logVerb string,
) error {
	if len(trackIDs) == 0 {
		return nil
	}

	for i := 0; i < len(trackIDs); i += batchSize {
		end := min(i+batchSize, len(trackIDs))
		batch := trackIDs[i:end]

		if err := op(ctx, playlistID, batch); err != nil {
			return fmt.Errorf("%s tracks batch %d-%d to playlist %s: %w",
				operationName, i, end-1, playlistID, err)
		}

		logger.DebugContext(ctx, fmt.Sprintf("%s tracks batch to playlist", logVerb),
			"playlist_id", playlistID,
			"batch_start", i,
			"batch_end", end-1,
			"batch_size", len(batch))
	}

	logger.InfoContext(ctx, fmt.Sprintf("%s all tracks to playlist", logVerb),
		"playlist_id", playlistID,
		"total_tracks", len(trackIDs))

	return nil
}

// BatchAddTracks adds tracks to a playlist in batches. The batch size is
// determined by the Spotify API limit (100 tracks per request). Tracks are
// added in order, with each batch being a separate API call.
func BatchAddTracks(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	playlistID spotify.ID,
	trackIDs []spotify.ID,
	batchSize int,
) error {
	return performBatchOperation(
		ctx,
		logger,
		playlistID,
		trackIDs,
		batchSize,
		func(ctx context.Context, playlistID spotify.ID, batch []spotify.ID) error {
			return withLegacyFallback(ctx, logger, "POST /playlists/{id}/items",
				func() error {
					_, err := client.AddItemsToPlaylist(ctx, playlistID, trackURIs(batch)...)
					return err
				},
				func() error {
					//nolint:staticcheck // SA1019: the legacy endpoint is the deliberate fallback.
					_, err := client.AddTracksToPlaylist(ctx, playlistID, batch...)
					return err
				},
			)
		},
		"adding",
		"added",
	)
}

// BatchRemoveTracks removes tracks from a playlist in batches. The batch size
// is determined by the Spotify API limit (100 tracks per request). Tracks are
// removed in order, with each batch being a separate API call.
func BatchRemoveTracks(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	playlistID spotify.ID,
	trackIDs []spotify.ID,
	batchSize int,
) error {
	return performBatchOperation(
		ctx,
		logger,
		playlistID,
		trackIDs,
		batchSize,
		func(ctx context.Context, playlistID spotify.ID, batch []spotify.ID) error {
			return withLegacyFallback(ctx, logger, "DELETE /playlists/{id}/items",
				func() error {
					_, err := client.RemoveItemsFromPlaylist(ctx, playlistID, trackURIs(batch)...)
					return err
				},
				func() error {
					//nolint:staticcheck // SA1019: the legacy endpoint is the deliberate fallback.
					_, err := client.RemoveTracksFromPlaylist(ctx, playlistID, batch...)
					return err
				},
			)
		},
		"removing",
		"removed",
	)
}

// UnfollowPlaylist unfollows (deletes) a playlist from the user's library.
// This is used for cleaning up stale genre playlists that are no longer needed.
// It uses DELETE /me/library and falls back to the legacy
// DELETE /playlists/{id}/followers.
func UnfollowPlaylist(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	playlistID spotify.ID,
) error {
	err := withLegacyFallback(ctx, logger, "DELETE /me/library",
		func() error {
			return client.RemoveItemsFromLibrary(ctx, spotify.URI("spotify:playlist:"+string(playlistID)))
		},
		func() error {
			return client.UnfollowPlaylist(ctx, playlistID) //nolint:staticcheck // SA1019: deliberate fallback.
		},
	)
	if err != nil {
		return fmt.Errorf("unfollowing playlist %s: %w", playlistID, err)
	}

	logger.InfoContext(ctx, "unfollowed playlist",
		"playlist_id", playlistID)

	return nil
}

// IsPlaylistNotFoundError reports whether err indicates that a playlist was
// not found (404 or invalid ID). This is used for cache fallback logic.
// It delegates to cache.IsCacheStaleError since they check for the same conditions.
func IsPlaylistNotFoundError(err error) bool {
	return cache.IsCacheStaleError(err)
}

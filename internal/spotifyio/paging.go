// Package spotifyio provides Spotify API client wrappers with rate limiting,
// retry logic, and optimized paging helpers.
package spotifyio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/omerzamir/spotify/v2"
)

const (
	likedTracksPageSize = 50
	artistsBatchSize    = 50

	// GET /playlists/{id}/items returns at most 50 items per page; the legacy
	// /tracks endpoint returns up to 100.
	playlistItemsPageSize        = 50
	legacyPlaylistTracksPageSize = 100
)

// LikedTrackData holds the essential data extracted from a liked track for
// deduplication and classification purposes.
type LikedTrackData struct {
	TrackID    spotify.ID
	ArtistIDs  []spotify.ID
	ISRC       string
	AddedAt    string
	IsPlayable bool
}

// FetchAllLikedTracks paginates through all user's liked tracks, extracting
// track IDs, artist IDs, ISRC codes, and playability status. Uses Fields()
// optimization to minimize payload size. Returns all tracks with their metadata.
func FetchAllLikedTracks(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
) ([]LikedTrackData, error) {
	tracks, err := fetchLikedTracks(ctx, logger, client, time.Time{})
	if err != nil {
		return nil, err
	}

	logger.InfoContext(ctx, "fetched all liked tracks",
		"total", len(tracks))

	return tracks, nil
}

// FetchLikedTracksSince pages through the user's liked tracks newest-first and
// stops at the first track added strictly before since. Tracks added exactly
// at since are included, because bulk saves share one timestamp and some of
// them may not have been seen yet. A zero since fetches everything.
//
// Spotify does not document the sort order of GET /me/tracks; it has always
// been newest-first. Callers must treat the result as a best-effort delta and
// rely on a full fetch for reconciliation.
func FetchLikedTracksSince(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	since time.Time,
) ([]LikedTrackData, error) {
	tracks, err := fetchLikedTracks(ctx, logger, client, since)
	if err != nil {
		return nil, err
	}

	logger.InfoContext(ctx, "fetched liked tracks since checkpoint",
		"since", since,
		"total", len(tracks))

	return tracks, nil
}

// fetchLikedTracks pages through liked tracks newest-first, stopping at the
// first track added before since (never stopping when since is zero).
func fetchLikedTracks(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	since time.Time,
) ([]LikedTrackData, error) {
	fields := "items(added_at,track(id,artists(id),external_ids(isrc),is_playable)),next,total"
	page, err := client.CurrentUsersTracks(
		ctx,
		spotify.Limit(likedTracksPageSize),
		spotify.Fields(fields),
	)
	if err != nil {
		return nil, fmt.Errorf("fetching first page of liked tracks: %w", err)
	}

	var allTracks []LikedTrackData

	for {
		for _, savedTrack := range page.Tracks {
			if !since.IsZero() && addedBefore(ctx, logger, savedTrack.AddedAt, since) {
				return allTracks, nil
			}

			if savedTrack.ID == "" {
				logger.WarnContext(ctx, "skipping empty track in liked songs")
				continue
			}

			artistIDs := make([]spotify.ID, 0, len(savedTrack.Artists))
			for _, artist := range savedTrack.Artists {
				artistIDs = append(artistIDs, artist.ID)
			}

			isPlayable := savedTrack.IsPlayable == nil || *savedTrack.IsPlayable

			allTracks = append(allTracks, LikedTrackData{
				TrackID:    savedTrack.ID,
				ArtistIDs:  artistIDs,
				ISRC:       savedTrack.ExternalIDs.ISRC,
				AddedAt:    savedTrack.AddedAt,
				IsPlayable: isPlayable,
			})
		}

		err = client.NextPage(ctx, page)
		if errors.Is(err, spotify.ErrNoMorePages) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("paging liked tracks: %w", err)
		}
	}

	return allTracks, nil
}

// addedBefore reports whether the RFC3339 addedAt timestamp is strictly before
// since. Unparseable timestamps never stop paging.
func addedBefore(ctx context.Context, logger *slog.Logger, addedAt string, since time.Time) bool {
	t, err := time.Parse(time.RFC3339, addedAt)
	if err != nil {
		logger.WarnContext(ctx, "unparseable added_at on liked track",
			"added_at", addedAt,
			"error", err)
		return false
	}

	return t.Before(since)
}

// PlaylistTrackData holds the identity of a track in a playlist: its Spotify
// ID and, when Spotify returns one, its ISRC.
type PlaylistTrackData struct {
	TrackID spotify.ID
	ISRC    string
}

// FetchPlaylistTracks paginates through a playlist's items and returns each
// track's ID and ISRC in playlist order. Skips episodes, unavailable tracks and
// local files (which have no Spotify ID). Uses Fields() so only the ID, type
// and ISRC of each item are transferred.
//
// It reads GET /playlists/{id}/items and falls back to the legacy
// GET /playlists/{id}/tracks when the new endpoint is unavailable, which is
// the case for playlists the user does not own or collaborate on.
func FetchPlaylistTracks(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	playlistID spotify.ID,
) ([]PlaylistTrackData, error) {
	var tracks []PlaylistTrackData

	err := withLegacyFallback(ctx, logger, "GET /playlists/{id}/items",
		func() error {
			var err error
			tracks, err = fetchPlaylistItems(ctx, logger, client, playlistID)
			return err
		},
		func() error {
			var err error
			tracks, err = fetchPlaylistTracksLegacy(ctx, client, playlistID)
			return err
		},
	)
	if err != nil {
		return nil, err
	}

	logger.InfoContext(ctx, "fetched playlist tracks",
		"playlist_id", playlistID,
		"total", len(tracks))

	return tracks, nil
}

// fetchPlaylistItems reads a playlist through GET /playlists/{id}/items.
func fetchPlaylistItems(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	playlistID spotify.ID,
) ([]PlaylistTrackData, error) {
	page, err := client.GetPlaylistItems(
		ctx,
		playlistID,
		spotify.Limit(playlistItemsPageSize),
		spotify.Fields("items(item(type,id,external_ids(isrc))),next,total"),
	)
	if err != nil {
		return nil, fmt.Errorf("fetching first page of playlist items: %w", err)
	}

	var tracks []PlaylistTrackData

	for {
		for _, item := range page.Items {
			track := item.Track.Track

			switch {
			case item.Track.Episode != nil:
				logger.DebugContext(ctx, "skipping episode in playlist", "playlist_id", playlistID)
			case track == nil:
				logger.WarnContext(ctx, "skipping null or unavailable track in playlist", "playlist_id", playlistID)
			case track.ID == "":
				logger.DebugContext(ctx, "skipping local file in playlist", "playlist_id", playlistID)
			default:
				tracks = append(tracks, PlaylistTrackData{TrackID: track.ID, ISRC: track.ExternalIDs.ISRC})
			}
		}

		err = client.NextPage(ctx, page)
		if errors.Is(err, spotify.ErrNoMorePages) {
			return tracks, nil
		}
		if err != nil {
			return nil, fmt.Errorf("paging playlist items: %w", err)
		}
	}
}

// fetchPlaylistTracksLegacy reads a playlist through the legacy
// GET /playlists/{id}/tracks endpoint, which only returns tracks.
func fetchPlaylistTracksLegacy(
	ctx context.Context,
	client *spotify.Client,
	playlistID spotify.ID,
) ([]PlaylistTrackData, error) {
	//nolint:staticcheck // SA1019: the legacy endpoint is the deliberate fallback.
	page, err := client.GetPlaylistTracks(
		ctx,
		playlistID,
		spotify.Limit(legacyPlaylistTracksPageSize),
		spotify.Fields("items(track(type,id,external_ids(isrc))),next,total"),
	)
	if err != nil {
		return nil, fmt.Errorf("fetching first page of playlist tracks: %w", err)
	}

	var tracks []PlaylistTrackData

	for {
		for _, item := range page.Tracks {
			// Unavailable tracks decode as an empty track and local files have
			// no ID; neither can be added to another playlist.
			if item.Track.ID != "" && item.Track.Type != "episode" {
				tracks = append(tracks, PlaylistTrackData{TrackID: item.Track.ID, ISRC: item.Track.ExternalIDs.ISRC})
			}
		}

		err = client.NextPage(ctx, page)
		if errors.Is(err, spotify.ErrNoMorePages) {
			return tracks, nil
		}
		if err != nil {
			return nil, fmt.Errorf("paging playlist tracks: %w", err)
		}
	}
}

// FetchPlaylistTrackIDs paginates through a playlist's items and returns
// all track IDs. Skips episodes and other non-track items.
func FetchPlaylistTrackIDs(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	playlistID spotify.ID,
) ([]spotify.ID, error) {
	tracks, err := FetchPlaylistTracks(ctx, logger, client, playlistID)
	if err != nil {
		return nil, err
	}

	trackIDs := make([]spotify.ID, len(tracks))
	for i, track := range tracks {
		trackIDs[i] = track.TrackID
	}

	return trackIDs, nil
}

// BatchGetArtists fetches artist details in batches of up to 50 artists
// per API call. Returns a map from artist ID to FullArtist. Artists that
// are not found (deleted/merged) are omitted from the result.
func BatchGetArtists(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	artistIDs []spotify.ID,
) (map[spotify.ID]*spotify.FullArtist, error) {
	result := make(map[spotify.ID]*spotify.FullArtist, len(artistIDs))

	for i := 0; i < len(artistIDs); i += artistsBatchSize {
		end := min(i+artistsBatchSize, len(artistIDs))
		batch := artistIDs[i:end]
		// Batch GET /artists is deprecated and removed for Development Mode
		// apps created after February 2026; it still works for this app.
		// Moving to per-artist requests is tracked as a follow-up.
		artists, err := client.GetArtists(ctx, batch...) //nolint:staticcheck // SA1019: see comment above.
		if err != nil {
			return nil, fmt.Errorf("fetching artists batch %d-%d: %w", i, end-1, err)
		}

		for _, artist := range artists {
			if artist != nil {
				result[artist.ID] = artist
			}
		}

		if len(artists) < len(batch) {
			logger.WarnContext(ctx,
				"some artists not found in batch",
				"requested", len(batch),
				"returned", len(artists),
				"batch_start", i)
		}
	}

	logger.InfoContext(ctx, "fetched artist details",
		"requested", len(artistIDs),
		"found", len(result))

	return result, nil
}

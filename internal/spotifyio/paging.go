// Package spotifyio provides Spotify API client wrappers with rate limiting,
// retry logic, and optimized paging helpers.
package spotifyio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/zmb3/spotify/v2"
)

const (
	likedTracksPageSize   = 50
	playlistItemsPageSize = 100
	artistsBatchSize      = 50
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
	fields := "items(track(id,artists(id),external_ids(isrc),is_playable)),added_at,next,total"
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
			if savedTrack.ID == "" {
				logger.WarnContext(ctx, "skipping empty track in liked songs")
				continue
			}

			artistIDs := make([]spotify.ID, 0, len(savedTrack.Artists))
			for _, artist := range savedTrack.Artists {
				artistIDs = append(artistIDs, artist.ID)
			}

			isrc := ""
			if savedTrack.ExternalIDs != nil {
				isrc = savedTrack.ExternalIDs["isrc"]
			}

			isPlayable := savedTrack.IsPlayable == nil || *savedTrack.IsPlayable

			allTracks = append(allTracks, LikedTrackData{
				TrackID:    savedTrack.ID,
				ArtistIDs:  artistIDs,
				ISRC:       isrc,
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

	logger.InfoContext(ctx, "fetched all liked tracks",
		"total", len(allTracks))

	return allTracks, nil
}

// FetchPlaylistTrackIDs paginates through a playlist's items and returns
// all track IDs. Skips episodes and other non-track items.
func FetchPlaylistTrackIDs(
	ctx context.Context,
	logger *slog.Logger,
	client *spotify.Client,
	playlistID spotify.ID,
) ([]spotify.ID, error) {
	page, err := client.GetPlaylistItems(
		ctx,
		playlistID,
		spotify.Limit(playlistItemsPageSize),
	)
	if err != nil {
		return nil, fmt.Errorf("fetching first page of playlist items: %w", err)
	}

	var trackIDs []spotify.ID

	for {
		for _, item := range page.Items {
			if item.Track.Episode != nil {
				logger.DebugContext(ctx, "skipping episode in playlist",
					"playlist_id", playlistID)
				continue
			}

			if item.Track.Track == nil {
				logger.WarnContext(ctx, "skipping null or unavailable track in playlist",
					"playlist_id", playlistID)
				continue
			}

			trackIDs = append(trackIDs, item.Track.Track.ID)
		}

		err = client.NextPage(ctx, page)
		if errors.Is(err, spotify.ErrNoMorePages) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("paging playlist items: %w", err)
		}
	}

	logger.InfoContext(ctx, "fetched playlist track IDs",
		"playlist_id", playlistID,
		"total", len(trackIDs))

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
		artists, err := client.GetArtists(ctx, batch...)
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

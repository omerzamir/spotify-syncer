package spotifyio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/omerzamir/spotify/v2"
)

// withLegacyFallback runs current, which uses one of the endpoints Spotify
// introduced in February 2026, and falls back to legacy, which uses the
// endpoint it replaces, when the current one is unavailable to this app.
//
// The fallback is decided per call rather than once per run because some
// failures are resource-specific: GET /playlists/{id}/items returns 403 for
// playlists the user does not own, while the legacy endpoint still serves
// them to apps that have it.
func withLegacyFallback(
	ctx context.Context,
	logger *slog.Logger,
	endpoint string,
	current func() error,
	legacy func() error,
) error {
	err := current()
	if err == nil || !endpointUnavailable(err) {
		return err
	}

	logger.DebugContext(ctx, "current endpoint unavailable, using legacy endpoint",
		"endpoint", endpoint,
		"error", err)

	if legacyErr := legacy(); legacyErr != nil {
		return fmt.Errorf("legacy endpoint for %s: %w (current endpoint: %w)", endpoint, legacyErr, err)
	}

	return nil
}

// endpointUnavailable reports whether err is a client error that the legacy
// endpoint might not hit: any 4xx except 401 (bad token, which the legacy
// endpoint would reject too) and 429 (handled by the rate-limited transport).
func endpointUnavailable(err error) bool {
	apiErr, ok := errors.AsType[spotify.Error](err)
	if !ok {
		return false
	}

	switch apiErr.Status {
	case http.StatusUnauthorized, http.StatusTooManyRequests:
		return false
	default:
		return apiErr.Status >= http.StatusBadRequest && apiErr.Status < http.StatusInternalServerError
	}
}

// trackURIs converts track IDs to Spotify track URIs.
func trackURIs(ids []spotify.ID) []spotify.URI {
	uris := make([]spotify.URI, len(ids))
	for i, id := range ids {
		uris[i] = spotify.URI("spotify:track:" + string(id))
	}
	return uris
}

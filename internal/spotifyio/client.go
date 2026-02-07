// Package spotifyio provides Spotify API client wrappers with rate limiting,
// retry logic, and optimized paging helpers.
package spotifyio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/zmb3/spotify/v2"
	"golang.org/x/oauth2"
	"golang.org/x/time/rate"

	"spotify-syncer/internal/config"
)

const (
	httpStatusTooManyRequests = 429
	retryAfterHeader          = "Retry-After"
)

// rateLimitedTransport wraps an http.RoundTripper with rate limiting and
// automatic retry on 429 responses. It uses a token bucket rate limiter
// configured via RateLimitConfig.
type rateLimitedTransport struct {
	base       http.RoundTripper
	limiter    *rate.Limiter
	maxRetries int
	logger     *slog.Logger
}

// RoundTrip implements http.RoundTripper, applying rate limiting and
// handling 429 responses with Retry-After header parsing.
func (r *rateLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if err := r.limiter.Wait(req.Context()); err != nil {
			return nil, fmt.Errorf("rate limiter wait: %w", err)
		}

		resp, err := r.base.RoundTrip(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != httpStatusTooManyRequests {
			return resp, nil
		}

		retryAfter := parseRetryAfter(resp.Header.Get(retryAfterHeader))
		if retryAfter <= 0 {
			retryAfter = time.Duration(attempt+1) * time.Second
		}

		if attempt < r.maxRetries {
			r.logger.WarnContext(req.Context(),
				"rate limited, retrying after delay",
				"attempt", attempt+1,
				"max_retries", r.maxRetries,
				"retry_after", retryAfter,
				"url", req.URL.String())

			_ = resp.Body.Close()

			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(retryAfter):
			}
		} else {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("rate limit exceeded after %d retries", r.maxRetries)
		}
	}

	return nil, errors.New("unexpected retry loop exit")
}

// parseRetryAfter parses the Retry-After header value, which can be either
// a number of seconds (as a string) or an HTTP date. Returns 0 if parsing fails.
func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}

	if t, err := http.ParseTime(value); err == nil {
		delay := time.Until(t)
		if delay > 0 {
			return delay
		}
	}

	return 0
}

// NewClient creates a rate-limited Spotify client from an OAuth2 token source.
// The client wraps the OAuth2 HTTP client with rate limiting configured via
// cfg.RateLimit. The returned client is ready to use for Spotify API calls.
func NewClient(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.Config,
	tokenSource oauth2.TokenSource,
) (*spotify.Client, error) {
	baseClient := oauth2.NewClient(ctx, tokenSource)

	limiter := rate.NewLimiter(
		rate.Limit(cfg.RateLimit.RequestsPerSecond),
		cfg.RateLimit.Burst,
	)

	transport := &rateLimitedTransport{
		base:       baseClient.Transport,
		limiter:    limiter,
		maxRetries: cfg.RateLimit.MaxRetries,
		logger:     logger,
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   baseClient.Timeout,
	}

	return spotify.New(httpClient), nil
}

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

	"spotify-syncer/internal/config"
	"spotify-syncer/internal/ratelimit"
)

const retryAfterHeader = "Retry-After"

// rateLimitedTransport wraps an http.RoundTripper with rate limiting and
// automatic retry on 429 responses, honouring the Retry-After header.
type rateLimitedTransport struct {
	base       http.RoundTripper
	limiter    *ratelimit.Limiter
	maxRetries int
	logger     *slog.Logger
}

// RoundTrip implements http.RoundTripper. Requests with a body are retried
// only when the body can be rewound via Request.GetBody, which is the case
// for every request built by http.NewRequest from an in-memory reader.
func (r *rateLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()

	for attempt := range r.maxRetries + 1 {
		if err := r.limiter.Wait(ctx); err != nil {
			return nil, err
		}

		attemptReq, err := rewindBody(req, attempt)
		if err != nil {
			return nil, err
		}

		resp, err := r.base.RoundTrip(attemptReq)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}

		_ = resp.Body.Close()

		if attempt == r.maxRetries {
			break
		}

		retryAfter := parseRetryAfter(resp.Header.Get(retryAfterHeader))
		if retryAfter <= 0 {
			retryAfter = time.Duration(attempt+1) * time.Second
		}

		r.logger.WarnContext(ctx, "rate limited, retrying after delay",
			"attempt", attempt+1,
			"max_retries", r.maxRetries,
			"retry_after", retryAfter,
			"url", req.URL.String())

		if err = sleep(ctx, retryAfter); err != nil {
			return nil, err
		}
	}

	return nil, fmt.Errorf("rate limit exceeded after %d retries", r.maxRetries)
}

// rewindBody returns the request to send for the given attempt. The first
// attempt uses req as is; later attempts get a clone with a fresh body.
func rewindBody(req *http.Request, attempt int) (*http.Request, error) {
	if attempt == 0 || req.Body == nil || req.Body == http.NoBody {
		return req, nil
	}

	if req.GetBody == nil {
		return nil, errors.New("cannot retry request: body is not rewindable")
	}

	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("rewinding request body: %w", err)
	}

	clone := req.Clone(req.Context())
	clone.Body = body

	return clone, nil
}

// sleep waits for d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting to retry: %w", context.Cause(ctx))
	}
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
// The client wraps the OAuth2 HTTP client with rate limiting and 429 retries
// configured via cfg.RateLimit.
func NewClient(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.RateLimitConfig,
	tokenSource oauth2.TokenSource,
) *spotify.Client {
	return spotify.New(newHTTPClient(oauth2.NewClient(ctx, tokenSource), logger, cfg))
}

// newHTTPClient wraps base's transport with rate limiting and retries.
func newHTTPClient(base *http.Client, logger *slog.Logger, cfg config.RateLimitConfig) *http.Client {
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	return &http.Client{
		Transport: &rateLimitedTransport{
			base:       transport,
			limiter:    ratelimit.New(cfg.RequestsPerSecond, cfg.Burst),
			maxRetries: cfg.MaxRetries,
			logger:     logger,
		},
		Timeout: base.Timeout,
	}
}

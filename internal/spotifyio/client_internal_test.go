package spotifyio

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"spotify-syncer/internal/config"
)

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func response(status int, header http.Header) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func TestTransportRetries429WithRetryAfterAndResendsBody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			bodies []string
			times  []time.Time
		)

		base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			b, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			bodies = append(bodies, string(b))
			times = append(times, time.Now())

			if len(bodies) == 1 {
				return response(http.StatusTooManyRequests, http.Header{"Retry-After": {"2"}}), nil
			}
			return response(http.StatusCreated, nil), nil
		})

		client := newHTTPClient(&http.Client{Transport: base}, slog.New(slog.DiscardHandler),
			config.RateLimitConfig{RequestsPerSecond: 100, Burst: 10, MaxRetries: 3})

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.test/items",
			strings.NewReader(`{"uris":["spotify:track:a"]}`))
		if err != nil {
			t.Fatal(err)
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
		}
		if len(bodies) != 2 || bodies[0] != bodies[1] || bodies[1] == "" {
			t.Errorf("bodies = %q, want the same non-empty body twice", bodies)
		}
		if gap := times[1].Sub(times[0]); gap != 2*time.Second {
			t.Errorf("retry after %v, want 2s from Retry-After", gap)
		}
	})
}

func TestTransportGivesUpAfterMaxRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		base := roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return response(http.StatusTooManyRequests, nil), nil
		})

		client := newHTTPClient(&http.Client{Transport: base}, slog.New(slog.DiscardHandler),
			config.RateLimitConfig{RequestsPerSecond: 100, Burst: 10, MaxRetries: 2})

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.test/me", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			t.Fatal("Do() error = nil, want rate limit error")
		}
		if calls != 3 {
			t.Errorf("calls = %d, want 3 (1 + 2 retries)", calls)
		}
	})
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		value string
		want  time.Duration
	}{
		{value: "", want: 0},
		{value: "3", want: 3 * time.Second},
		{value: "garbage", want: 0},
	}

	for _, tt := range tests {
		if got := parseRetryAfter(tt.value); got != tt.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

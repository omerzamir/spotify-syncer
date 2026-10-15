package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/zmb3/spotify/v2"
	"golang.org/x/oauth2"

	"spotify-syncer/internal/auth"
	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/config"
	"spotify-syncer/internal/spotifyio"
)

// logFormat selects the log handler: text for interactive commands, JSON for
// commands whose stdout is machine-readable.
type logFormat int

const (
	logJSON logFormat = iota
	logText
)

// environment holds the configuration, logger and state store shared by
// every command.
type environment struct {
	cfg    config.Config
	logger *slog.Logger
	store  *cache.Store
}

// loadEnvironment loads and validates the configuration (including Spotify
// credentials), builds the logger and opens the state store.
func loadEnvironment(configPath string, format logFormat, stderr io.Writer) (environment, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return environment{}, fmt.Errorf("%w: %w", errConfig, err)
	}

	if err = cfg.ValidateCredentials(); err != nil {
		return environment{}, fmt.Errorf("%w: %w", errConfig, err)
	}

	logger := newLogger(stderr, format, cfg.LogLevel)

	store, err := cache.NewStore(cfg.StateDir, logger)
	if err != nil {
		return environment{}, fmt.Errorf("creating cache store: %w", err)
	}

	return environment{cfg: cfg, logger: logger, store: store}, nil
}

// session is an environment with an authenticated Spotify client.
type session struct {
	environment

	client      *spotify.Client
	tokenSource oauth2.TokenSource
	token       *oauth2.Token
}

// openSession loads the environment and the saved OAuth token and returns a
// session with a rate-limited Spotify client. Call close when done so a token
// refreshed during the run is persisted.
func openSession(ctx context.Context, configPath string, stderr io.Writer) (*session, error) {
	env, err := loadEnvironment(configPath, logJSON, stderr)
	if err != nil {
		return nil, err
	}

	token, err := env.store.LoadToken()
	if err != nil {
		return nil, fmt.Errorf("loading token: %w", err)
	}

	if token == nil {
		return nil, fmt.Errorf("%w: no saved token found, run 'syncer auth' first", errConfig)
	}

	tokenSource := auth.NewTokenSource(ctx, env.cfg.Spotify, token)

	s := &session{
		environment: env,
		client:      spotifyio.NewClient(ctx, env.logger, env.cfg.RateLimit, tokenSource),
		tokenSource: tokenSource,
		token:       token,
	}

	// Refresh up front so an expired refresh token fails fast and a new
	// access token is saved even if the command later fails.
	if err = s.saveRefreshedToken(); err != nil {
		return nil, err
	}

	return s, nil
}

// close persists the OAuth token if it was refreshed during the session.
func (s *session) close(ctx context.Context) {
	if err := s.saveRefreshedToken(); err != nil {
		s.logger.WarnContext(ctx, "failed to save refreshed token", "error", err)
	}
}

func (s *session) saveRefreshedToken() error {
	current, err := s.tokenSource.Token()
	if err != nil {
		return fmt.Errorf("getting token: %w", err)
	}

	if current.AccessToken == s.token.AccessToken {
		return nil
	}

	if err = s.store.SaveToken(current); err != nil {
		return fmt.Errorf("saving refreshed token: %w", err)
	}

	s.token = current

	return nil
}

func newLogger(w io.Writer, format logFormat, level string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLogLevel(level)}

	if format == logText {
		return slog.New(slog.NewTextHandler(w, opts))
	}

	return slog.New(slog.NewJSONHandler(w, opts))
}

func parseLogLevel(level string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return slog.LevelInfo
	}
	return l
}

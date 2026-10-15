// Package config provides configuration loading and validation for the syncer.
package config

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	defaultPort                 = 8080
	defaultRedirectURI          = "http://127.0.0.1:8080/callback"
	defaultMirrorPlaylistName   = "Public Liked Songs"
	defaultConcurrency          = 2
	defaultBatchSize            = 100
	defaultMaxBuckets           = 10
	defaultMinBucketSize        = 20
	defaultGenresPerArtist      = 3
	defaultBucketPlaylistPrefix = "Liked -- "
	defaultArtistGenreTTL       = 720 * time.Hour
	defaultRequestsPerSecond    = 8.0
	defaultBurst                = 4
	defaultMaxRetries           = 5
	defaultStateDir             = "./state"
	defaultLogLevel             = "info"

	defaultConfigFile = "config.yaml"
	dotEnvFile        = ".env"
	envClientID       = "SPOTIFY_CLIENT_ID"
	envClientSecret   = "SPOTIFY_CLIENT_SECRET" //nolint:gosec // environment variable name, not a credential
)

// SpotifyConfig holds Spotify API credentials and connection settings.
type SpotifyConfig struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	RedirectURI  string `yaml:"redirect_uri"`
	Port         int    `yaml:"port"`
}

// SyncConfig holds mirror playlist synchronization settings.
type SyncConfig struct {
	MirrorPlaylistName string `yaml:"mirror_playlist_name"`
	Concurrency        int    `yaml:"concurrency"`
	BatchSize          int    `yaml:"batch_size"`
}

// GenreConfig holds genre bucket classification settings.
type GenreConfig struct {
	MaxBuckets           int           `yaml:"max_buckets"`
	MinBucketSize        int           `yaml:"min_bucket_size"`
	GenresPerArtist      int           `yaml:"genres_per_artist"`
	BucketPlaylistPrefix string        `yaml:"bucket_playlist_prefix"`
	ArtistGenreTTL       time.Duration `yaml:"artist_genre_ttl"`
}

// RateLimitConfig holds Spotify API rate limiting settings.
type RateLimitConfig struct {
	RequestsPerSecond float64 `yaml:"requests_per_second"`
	Burst             int     `yaml:"burst"`
	MaxRetries        int     `yaml:"max_retries"`
}

// Config holds the complete application configuration.
type Config struct {
	Spotify   SpotifyConfig   `yaml:"spotify"`
	Sync      SyncConfig      `yaml:"sync"`
	Genre     GenreConfig     `yaml:"genre"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
	StateDir  string          `yaml:"state_dir"`
	LogLevel  string          `yaml:"log_level"`
}

// Default returns the configuration used when no config file is present.
func Default() Config {
	return Config{
		Spotify: SpotifyConfig{
			RedirectURI: defaultRedirectURI,
			Port:        defaultPort,
		},
		Sync: SyncConfig{
			MirrorPlaylistName: defaultMirrorPlaylistName,
			Concurrency:        defaultConcurrency,
			BatchSize:          defaultBatchSize,
		},
		Genre: GenreConfig{
			MaxBuckets:           defaultMaxBuckets,
			MinBucketSize:        defaultMinBucketSize,
			GenresPerArtist:      defaultGenresPerArtist,
			BucketPlaylistPrefix: defaultBucketPlaylistPrefix,
			ArtistGenreTTL:       defaultArtistGenreTTL,
		},
		RateLimit: RateLimitConfig{
			RequestsPerSecond: defaultRequestsPerSecond,
			Burst:             defaultBurst,
			MaxRetries:        defaultMaxRetries,
		},
		StateDir: defaultStateDir,
		LogLevel: defaultLogLevel,
	}
}

// Load builds the configuration from defaults, the YAML config file and the
// environment, in increasing order of precedence. Variables from a .env file
// in the working directory are loaded first without overriding variables that
// are already set. When path is empty, config.yaml in the working directory is
// used if it exists. Unknown keys in the config file are an error.
func Load(path string) (Config, error) {
	if err := LoadDotEnv(dotEnvFile); err != nil {
		return Config{}, err
	}

	cfg := Default()

	if err := readConfigFile(path, &cfg); err != nil {
		return Config{}, err
	}

	cfg.Spotify.ClientID = cmp.Or(os.Getenv(envClientID), cfg.Spotify.ClientID)
	cfg.Spotify.ClientSecret = cmp.Or(os.Getenv(envClientSecret), cfg.Spotify.ClientSecret)

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validating config: %w", err)
	}

	return cfg, nil
}

// readConfigFile decodes the YAML file at path into cfg, keeping the values
// already in cfg for keys the file omits. A missing default config file is
// not an error; a missing explicit one is.
func readConfigFile(path string, cfg *Config) error {
	explicit := path != ""
	if !explicit {
		path = defaultConfigFile
	}

	f, err := os.Open(path)
	if err != nil {
		if !explicit && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("opening config file: %w", err)
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)

	if err = dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("decoding config file %q: %w", path, err)
	}

	return nil
}

// Validate checks that all configuration values are within acceptable ranges.
func (c Config) Validate() error {
	if err := c.Sync.validate(); err != nil {
		return err
	}

	if err := c.Genre.validate(); err != nil {
		return err
	}

	if err := c.RateLimit.validate(); err != nil {
		return err
	}

	if c.Spotify.Port <= 0 {
		return errors.New("spotify.port must be positive")
	}

	if c.StateDir == "" {
		return errors.New("state_dir must not be empty")
	}

	return nil
}

// ValidateCredentials checks that required Spotify API credentials are configured.
func (c Config) ValidateCredentials() error {
	if c.Spotify.ClientID == "" {
		return errors.New(envClientID + " is required")
	}

	if c.Spotify.ClientSecret == "" {
		return errors.New(envClientSecret + " is required")
	}

	return nil
}

func (s SyncConfig) validate() error {
	if s.Concurrency <= 0 {
		return errors.New("sync.concurrency must be positive")
	}

	if s.BatchSize <= 0 {
		return errors.New("sync.batch_size must be positive")
	}

	return nil
}

func (g GenreConfig) validate() error {
	if g.MaxBuckets <= 0 {
		return errors.New("genre.max_buckets must be positive")
	}

	if g.MinBucketSize <= 0 {
		return errors.New("genre.min_bucket_size must be positive")
	}

	if g.GenresPerArtist <= 0 {
		return errors.New("genre.genres_per_artist must be positive")
	}

	if g.ArtistGenreTTL <= 0 {
		return errors.New("genre.artist_genre_ttl must be positive")
	}

	return nil
}

func (r RateLimitConfig) validate() error {
	if r.RequestsPerSecond <= 0 {
		return errors.New("rate_limit.requests_per_second must be positive")
	}

	if r.Burst <= 0 {
		return errors.New("rate_limit.burst must be positive")
	}

	if r.MaxRetries < 0 {
		return errors.New("rate_limit.max_retries must be non-negative")
	}

	return nil
}

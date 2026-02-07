// Package config provides configuration loading and validation for the syncer.
package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/viper"
	"github.com/subosito/gotenv"
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
)

// SpotifyConfig holds Spotify API credentials and connection settings.
type SpotifyConfig struct {
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	RedirectURI  string `mapstructure:"redirect_uri"`
	Port         int    `mapstructure:"port"`
}

// SyncConfig holds mirror playlist synchronization settings.
type SyncConfig struct {
	MirrorPlaylistName string `mapstructure:"mirror_playlist_name"`
	Concurrency        int    `mapstructure:"concurrency"`
	BatchSize          int    `mapstructure:"batch_size"`
}

// GenreConfig holds genre bucket classification settings.
type GenreConfig struct {
	MaxBuckets           int           `mapstructure:"max_buckets"`
	MinBucketSize        int           `mapstructure:"min_bucket_size"`
	GenresPerArtist      int           `mapstructure:"genres_per_artist"`
	BucketPlaylistPrefix string        `mapstructure:"bucket_playlist_prefix"`
	ArtistGenreTTL       time.Duration `mapstructure:"artist_genre_ttl"`
}

// RateLimitConfig holds Spotify API rate limiting settings.
type RateLimitConfig struct {
	RequestsPerSecond float64 `mapstructure:"requests_per_second"`
	Burst             int     `mapstructure:"burst"`
	MaxRetries        int     `mapstructure:"max_retries"`
}

// Config holds the complete application configuration.
type Config struct {
	Spotify   SpotifyConfig   `mapstructure:"spotify"`
	Sync      SyncConfig      `mapstructure:"sync"`
	Genre     GenreConfig     `mapstructure:"genre"`
	RateLimit RateLimitConfig `mapstructure:"rate_limit"`
	StateDir  string          `mapstructure:"state_dir"`
	LogLevel  string          `mapstructure:"log_level"`
}

// Load reads configuration from the given YAML file path and merges
// environment variables for sensitive fields. When path is empty, it
// searches the working directory for config.yaml and falls back to
// defaults if no file is found.
func Load(path string) (Config, error) {
	loadEnvFile()

	v := viper.New()

	setDefaults(v)
	configureSource(v, path)

	if err := readConfig(v, path); err != nil {
		return Config{}, err
	}

	if err := bindEnvVars(v); err != nil {
		return Config{}, fmt.Errorf("binding environment variables: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshaling config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validating config: %w", err)
	}

	return cfg, nil
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
		return errors.New("SPOTIFY_CLIENT_ID is required")
	}

	if c.Spotify.ClientSecret == "" {
		return errors.New("SPOTIFY_CLIENT_SECRET is required")
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

func setDefaults(v *viper.Viper) {
	v.SetDefault("spotify.redirect_uri", defaultRedirectURI)
	v.SetDefault("spotify.port", defaultPort)
	v.SetDefault("sync.mirror_playlist_name", defaultMirrorPlaylistName)
	v.SetDefault("sync.concurrency", defaultConcurrency)
	v.SetDefault("sync.batch_size", defaultBatchSize)
	v.SetDefault("genre.max_buckets", defaultMaxBuckets)
	v.SetDefault("genre.min_bucket_size", defaultMinBucketSize)
	v.SetDefault("genre.genres_per_artist", defaultGenresPerArtist)
	v.SetDefault("genre.bucket_playlist_prefix", defaultBucketPlaylistPrefix)
	v.SetDefault("genre.artist_genre_ttl", defaultArtistGenreTTL)
	v.SetDefault("rate_limit.requests_per_second", defaultRequestsPerSecond)
	v.SetDefault("rate_limit.burst", defaultBurst)
	v.SetDefault("rate_limit.max_retries", defaultMaxRetries)
	v.SetDefault("state_dir", defaultStateDir)
	v.SetDefault("log_level", defaultLogLevel)
}

func configureSource(v *viper.Viper, path string) {
	if path != "" {
		v.SetConfigFile(path)
		return
	}

	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
}

func readConfig(v *viper.Viper, explicitPath string) error {
	err := v.ReadInConfig()
	if err == nil {
		return nil
	}

	if explicitPath != "" {
		return fmt.Errorf("reading config file %q: %w", explicitPath, err)
	}

	var notFoundErr viper.ConfigFileNotFoundError
	if !errors.As(err, &notFoundErr) {
		return fmt.Errorf("reading config file: %w", err)
	}

	return nil
}

func loadEnvFile() {
	envPath := ".env"
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		return
	}

	if err := gotenv.Load(envPath); err != nil {
		return
	}
}

func bindEnvVars(v *viper.Viper) error {
	if err := v.BindEnv("spotify.client_id", "SPOTIFY_CLIENT_ID"); err != nil {
		return fmt.Errorf("binding SPOTIFY_CLIENT_ID: %w", err)
	}

	if err := v.BindEnv("spotify.client_secret", "SPOTIFY_CLIENT_SECRET"); err != nil {
		return fmt.Errorf("binding SPOTIFY_CLIENT_SECRET: %w", err)
	}

	return nil
}

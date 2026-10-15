package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spotify-syncer/internal/config"
)

// chdirTemp switches to an empty temp directory for the test so Load does not
// pick up the developer's config.yaml or .env.
func chdirTemp(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func TestLoadDefaultsWithoutConfigFile(t *testing.T) {
	chdirTemp(t)
	t.Setenv("SPOTIFY_CLIENT_ID", "")
	t.Setenv("SPOTIFY_CLIENT_SECRET", "")

	got, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if want := config.Default(); got != want {
		t.Errorf("Load() = %+v, want defaults %+v", got, want)
	}
}

func TestLoadMergesFileOverDefaults(t *testing.T) {
	dir := chdirTemp(t)
	t.Setenv("SPOTIFY_CLIENT_ID", "")
	t.Setenv("SPOTIFY_CLIENT_SECRET", "")

	writeFile(t, filepath.Join(dir, "config.yaml"), `
sync:
  mirror_playlist_name: "My Mirror"
genre:
  artist_genre_ttl: 48h
rate_limit:
  requests_per_second: 2.5
`)

	got, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := config.Default()
	want.Sync.MirrorPlaylistName = "My Mirror"
	want.Genre.ArtistGenreTTL = 48 * time.Hour
	want.RateLimit.RequestsPerSecond = 2.5

	if got != want {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadEnvironmentOverridesFileAndDotEnv(t *testing.T) {
	dir := chdirTemp(t)
	t.Setenv("SPOTIFY_CLIENT_ID", "from-env")
	t.Setenv("SPOTIFY_CLIENT_SECRET", "")

	writeFile(t, filepath.Join(dir, "config.yaml"), "spotify:\n  client_id: from-file\n  client_secret: file-secret\n")
	writeFile(t, filepath.Join(dir, ".env"), "SPOTIFY_CLIENT_ID=from-dotenv\n")

	got, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Spotify.ClientID != "from-env" {
		t.Errorf("ClientID = %q, want from-env", got.Spotify.ClientID)
	}
	if got.Spotify.ClientSecret != "file-secret" {
		t.Errorf("ClientSecret = %q, want file-secret", got.Spotify.ClientSecret)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		path    string
		wantErr string
	}{
		{name: "unknown key", file: "sync:\n  batchsize: 10\n", wantErr: "field batchsize not found"},
		{name: "invalid value", file: "sync:\n  batch_size: 0\n", wantErr: "sync.batch_size must be positive"},
		{name: "bad duration", file: "genre:\n  artist_genre_ttl: soon\n", wantErr: "decoding config file"},
		{name: "missing explicit file", path: "nope.yaml", wantErr: "opening config file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := chdirTemp(t)
			if tt.file != "" {
				writeFile(t, filepath.Join(dir, "config.yaml"), tt.file)
			}

			_, err := config.Load(tt.path)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Load() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadDotEnv(t *testing.T) {
	dir := chdirTemp(t)
	t.Setenv("PRESET", "kept")

	writeFile(t, filepath.Join(dir, ".env"), `
# comment
PLAIN=value
export EXPORTED=yes
SPACED = padded   # trailing comment
DOUBLE="line\nbreak # not a comment"
SINGLE='lit\n'
EMPTY=
PRESET=overwritten
`)

	for _, key := range []string{"PLAIN", "EXPORTED", "SPACED", "DOUBLE", "SINGLE", "EMPTY"} {
		t.Setenv(key, "") // register cleanup, then unset so LoadDotEnv sets it
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}

	if err := config.LoadDotEnv(filepath.Join(dir, ".env")); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}

	want := map[string]string{
		"PLAIN":    "value",
		"EXPORTED": "yes",
		"SPACED":   "padded",
		"DOUBLE":   "line\nbreak # not a comment",
		"SINGLE":   `lit\n`,
		"EMPTY":    "",
		"PRESET":   "kept",
	}
	for key, value := range want {
		if got := os.Getenv(key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
}

func TestLoadDotEnvErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "no equals", content: "JUSTAKEY\n"},
		{name: "space in key", content: "BAD KEY=v\n"},
		{name: "unterminated double quote", content: "K=\"open\n"},
		{name: "unterminated single quote", content: "K='open\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			writeFile(t, path, tt.content)

			if err := config.LoadDotEnv(path); err == nil {
				t.Error("LoadDotEnv() error = nil, want error")
			}
		})
	}
}

func TestLoadDotEnvMissingFileIsNotAnError(t *testing.T) {
	if err := config.LoadDotEnv(filepath.Join(t.TempDir(), ".env")); err != nil {
		t.Errorf("LoadDotEnv() error = %v, want nil", err)
	}
}

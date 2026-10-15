// Package cache provides atomic JSON read/write operations and typed accessors
// for all syncer state files stored in the state directory.
package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omerzamir/spotify/v2"
	"golang.org/x/oauth2"
)

const (
	tokensFile       = "tokens.json"
	artistGenresFile = "artist_genres.json"
	trackArtistsFile = "track_artists.json"
	trackISRCFile    = "track_isrc.json"
	mirrorTracksFile = "mirror_tracks.json"
	bucketTracksFile = "bucket_tracks.json"
	playlistsFile    = "playlists.json"
	checkpointsFile  = "checkpoints.json"
	metricsFile      = "metrics_last.json"

	dirPermissions = 0o700
)

// ArtistGenreEntry holds cached genre tags for a single artist with a fetch
// timestamp for TTL-based expiration.
type ArtistGenreEntry struct {
	Genres    []string  `json:"genres"`
	FetchedAt time.Time `json:"fetched_at"`
}

// Checkpoint holds the fast-mode sync checkpoint.
type Checkpoint struct {
	LatestAddedAt time.Time `json:"latest_added_at"`

	// GenresPending is set while the genre playlists may be out of date with
	// the mirror: the mirror changed but genre sync failed or was disabled.
	// The next run with genres enabled syncs them even if the mirror did not
	// change, then clears the flag.
	GenresPending bool `json:"genres_pending,omitempty"`
}

// RunMetrics holds the summary statistics of a sync run.
type RunMetrics struct {
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	// Mode is the mode that actually ran: "full" or "fast". A fast run that
	// fell back to a full sync reports "full".
	Mode string `json:"mode"`
	// LikedTotal is the number of liked tracks fetched in full mode, or the
	// number known from the ISRC cache in fast mode.
	LikedTotal int `json:"liked_total"`
	// NewTracks is the number of liked tracks a fast run had not seen before.
	NewTracks      int      `json:"new_tracks"`
	DedupRemoved   int      `json:"dedup_removed"`
	MirrorAdded    int      `json:"mirror_added"`
	MirrorRemoved  int      `json:"mirror_removed"`
	BucketsCreated int      `json:"buckets_created"`
	BucketsRemoved int      `json:"buckets_removed"`
	DuplicateIDs   []string `json:"duplicate_ids,omitempty"`
}

// Store manages atomic reading and writing of JSON state files. It is not
// safe for concurrent use; callers must synchronize access externally.
type Store struct {
	dir    string
	logger *slog.Logger
}

// NewStore creates a Store backed by the given directory, creating it
// if it does not already exist.
func NewStore(dir string, logger *slog.Logger) (*Store, error) {
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return nil, fmt.Errorf("creating state directory %q: %w", dir, err)
	}

	return &Store{dir: dir, logger: logger}, nil
}

// Dir returns the state directory path.
func (s *Store) Dir() string {
	return s.dir
}

// LoadToken reads the persisted OAuth2 token. Returns nil when no token
// file exists.
func (s *Store) LoadToken() (*oauth2.Token, error) {
	return loadJSON[*oauth2.Token](s.path(tokensFile))
}

// SaveToken atomically writes the OAuth2 token.
func (s *Store) SaveToken(token *oauth2.Token) error {
	return saveJSON(s, tokensFile, token)
}

// LoadTrackArtists reads the track-to-artist ID mapping.
func (s *Store) LoadTrackArtists() (map[string][]string, error) {
	return loadJSON[map[string][]string](s.path(trackArtistsFile))
}

// SaveTrackArtists atomically writes the track-to-artist ID mapping.
func (s *Store) SaveTrackArtists(data map[string][]string) error {
	return saveJSON(s, trackArtistsFile, data)
}

// EvictTrackArtist removes a single track entry from the track-artist cache.
func (s *Store) EvictTrackArtist(trackID string) error {
	return evictMapKey(s, trackArtistsFile, trackID)
}

// LoadArtistGenres reads the artist genre cache.
func (s *Store) LoadArtistGenres() (map[string]ArtistGenreEntry, error) {
	return loadJSON[map[string]ArtistGenreEntry](s.path(artistGenresFile))
}

// SaveArtistGenres atomically writes the artist genre cache.
func (s *Store) SaveArtistGenres(data map[string]ArtistGenreEntry) error {
	return saveJSON(s, artistGenresFile, data)
}

// EvictArtistGenre removes a single artist entry from the genre cache.
func (s *Store) EvictArtistGenre(artistID string) error {
	return evictMapKey(s, artistGenresFile, artistID)
}

// LoadTrackISRC reads the track-to-ISRC code mapping.
func (s *Store) LoadTrackISRC() (map[string]string, error) {
	return loadJSON[map[string]string](s.path(trackISRCFile))
}

// SaveTrackISRC atomically writes the track-to-ISRC code mapping.
func (s *Store) SaveTrackISRC(data map[string]string) error {
	return saveJSON(s, trackISRCFile, data)
}

// EvictTrackISRC removes a single track entry from the ISRC cache.
func (s *Store) EvictTrackISRC(trackID string) error {
	return evictMapKey(s, trackISRCFile, trackID)
}

// LoadMirrorTracks reads the cached mirror playlist track IDs.
func (s *Store) LoadMirrorTracks() ([]string, error) {
	return loadJSON[[]string](s.path(mirrorTracksFile))
}

// SaveMirrorTracks atomically writes the mirror playlist track IDs.
func (s *Store) SaveMirrorTracks(trackIDs []string) error {
	return saveJSON(s, mirrorTracksFile, trackIDs)
}

// EvictMirrorTracks removes the entire mirror tracks cache file.
func (s *Store) EvictMirrorTracks() error {
	return removeFile(s.path(mirrorTracksFile))
}

// LoadBucketTracks reads the genre bucket to track ID mapping.
func (s *Store) LoadBucketTracks() (map[string][]string, error) {
	return loadJSON[map[string][]string](s.path(bucketTracksFile))
}

// SaveBucketTracks atomically writes the genre bucket to track ID mapping.
func (s *Store) SaveBucketTracks(data map[string][]string) error {
	return saveJSON(s, bucketTracksFile, data)
}

// EvictBucketTracks removes a single bucket from the bucket tracks cache.
func (s *Store) EvictBucketTracks(bucketName string) error {
	return evictMapKey(s, bucketTracksFile, bucketName)
}

// LoadPlaylists reads the playlist name-to-ID mapping.
func (s *Store) LoadPlaylists() (map[string]string, error) {
	return loadJSON[map[string]string](s.path(playlistsFile))
}

// SavePlaylists atomically writes the playlist name-to-ID mapping.
func (s *Store) SavePlaylists(data map[string]string) error {
	return saveJSON(s, playlistsFile, data)
}

// EvictPlaylist removes a single entry from the playlist cache.
func (s *Store) EvictPlaylist(name string) error {
	return evictMapKey(s, playlistsFile, name)
}

// LoadCheckpoint reads the fast-mode sync checkpoint.
func (s *Store) LoadCheckpoint() (Checkpoint, error) {
	return loadJSON[Checkpoint](s.path(checkpointsFile))
}

// SaveCheckpoint atomically writes the fast-mode sync checkpoint.
func (s *Store) SaveCheckpoint(cp Checkpoint) error {
	return saveJSON(s, checkpointsFile, cp)
}

// LoadMetrics reads the last run metrics.
func (s *Store) LoadMetrics() (RunMetrics, error) {
	return loadJSON[RunMetrics](s.path(metricsFile))
}

// SaveMetrics atomically writes the run metrics.
func (s *Store) SaveMetrics(m RunMetrics) error {
	return saveJSON(s, metricsFile, m)
}

// IsCacheStaleError reports whether err indicates that cached data is stale.
// It matches Spotify API errors with HTTP 404 status, and HTTP 400 whose
// message contains "invalid id".
func IsCacheStaleError(err error) bool {
	if err == nil {
		return false
	}

	spotifyErr, ok := errors.AsType[spotify.Error](err)
	if !ok {
		return false
	}

	if spotifyErr.Status == http.StatusNotFound {
		return true
	}

	return spotifyErr.Status == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(spotifyErr.Message), "invalid id")
}

func (s *Store) path(filename string) string {
	return filepath.Join(s.dir, filename)
}

// storable lists every type the Store persists as JSON. Map-shaped files are
// also handled as map[string]json.RawMessage when evicting single keys.
type storable interface {
	*oauth2.Token | Checkpoint | RunMetrics | []string |
		map[string]string | map[string][]string | map[string]ArtistGenreEntry |
		map[string]json.RawMessage
}

func saveJSON[T storable](s *Store, filename string, data T) error {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", filename, err)
	}

	b = append(b, '\n')

	return atomicWrite(s.path(filename), b)
}

func loadJSON[T storable](fpath string) (T, error) {
	var zero T

	data, err := os.ReadFile(fpath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return zero, nil
		}

		return zero, fmt.Errorf("reading %s: %w", filepath.Base(fpath), err)
	}

	if len(data) == 0 {
		return zero, nil
	}

	var result T
	if err = json.Unmarshal(data, &result); err != nil {
		return zero, fmt.Errorf("decoding %s: %w", filepath.Base(fpath), err)
	}

	return result, nil
}

func atomicWrite(target string, data []byte) error {
	dir := filepath.Dir(target)

	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}

	tmpPath := f.Name()
	success := false

	defer func() {
		if !success {
			_ = f.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	_, err = f.Write(data)
	if err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}

	err = f.Sync()
	if err != nil {
		return fmt.Errorf("syncing temp file: %w", err)
	}

	err = f.Close()
	if err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	err = os.Rename(tmpPath, target)
	if err != nil {
		return fmt.Errorf("renaming temp file to %s: %w", filepath.Base(target), err)
	}

	success = true

	return nil
}

// evictMapKey removes key from a map-shaped state file without decoding the
// other entries.
func evictMapKey(s *Store, filename, key string) error {
	m, err := loadJSON[map[string]json.RawMessage](s.path(filename))
	if err != nil {
		return err
	}

	if _, ok := m[key]; !ok {
		return nil
	}

	delete(m, key)

	return saveJSON(s, filename, m)
}

func removeFile(fpath string) error {
	err := os.Remove(fpath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", filepath.Base(fpath), err)
	}

	return nil
}

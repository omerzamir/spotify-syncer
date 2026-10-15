// Package spotifytest provides an in-memory fake of the Spotify Web API
// endpoints used by the syncer, for tests that exercise real code paths
// through a *spotify.Client.
package spotifytest

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omerzamir/spotify/v2"
)

const (
	trackURIPrefix    = "spotify:track:"
	playlistURIPrefix = "spotify:playlist:"

	// Page size limits and default enforced by the real API.
	maxLikedPageSize    = 50
	maxPlaylistPageSize = 50
	maxItemsPageSize    = 100
	defaultPageSize     = 20
)

// Track is a catalog track known to the fake server.
type Track struct {
	ID        string
	ISRC      string
	ArtistIDs []string
}

// SavedTrack is a track in the user's Liked Songs.
type SavedTrack struct {
	Track

	AddedAt time.Time
}

// Playlist is a playlist in the fake API. An empty Owner means the fake user
// owns it; any other owner makes it a followed playlist, which the
// February 2026 /items endpoints refuse with 403 like the real API.
type Playlist struct {
	ID          string
	Name        string
	Owner       string
	Description string
	Public      bool
	Tracks      []Track
}

// failAfterRule makes requests fail with status once okCalls have succeeded.
type failAfterRule struct {
	okCalls int
	status  int
}

// Server is a fake Spotify Web API. All methods are safe for concurrent use.
type Server struct {
	srv *httptest.Server

	mu           sync.Mutex
	userID       string
	now          time.Time
	catalog      map[string]Track
	liked        []SavedTrack
	playlists    []*Playlist
	artistGenres map[string][]string
	statuses     map[string]int
	failAfter    map[string]failAfterRule
	requests     []string
	nextID       int
}

// NewServer starts a fake Spotify server that is closed when the test ends.
func NewServer(t testing.TB) *Server {
	t.Helper()

	s := &Server{
		userID:       "fake-user",
		now:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		catalog:      make(map[string]Track),
		artistGenres: make(map[string][]string),
		statuses:     make(map[string]int),
		failAfter:    make(map[string]failAfterRule),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /me", s.handleMe)
	mux.HandleFunc("GET /me/tracks", s.handleLikedTracks)
	mux.HandleFunc("GET /me/tracks/contains", s.handleLikedContains)
	mux.HandleFunc("PUT /me/tracks", s.handleSaveTracks)
	mux.HandleFunc("GET /me/playlists", s.handleMyPlaylists)
	mux.HandleFunc("POST /users/{userID}/playlists", s.handleCreatePlaylist)
	mux.HandleFunc("GET /playlists/{id}", s.handleGetPlaylist)
	mux.HandleFunc("GET /playlists/{id}/tracks", s.handlePlaylistItems)
	mux.HandleFunc("POST /playlists/{id}/tracks", s.handleAddPlaylistItems)
	mux.HandleFunc("DELETE /playlists/{id}/tracks", s.handleRemovePlaylistItems)
	mux.HandleFunc("GET /playlists/{id}/items", s.ownedOnly(s.handlePlaylistItems))
	mux.HandleFunc("POST /playlists/{id}/items", s.ownedOnly(s.handleAddPlaylistItems))
	mux.HandleFunc("DELETE /playlists/{id}/items", s.ownedOnly(s.handleRemovePlaylistItems))
	mux.HandleFunc("POST /me/playlists", s.handleCreatePlaylist)
	mux.HandleFunc("PUT /me/library", s.handleSaveTracks)
	mux.HandleFunc("GET /me/library/contains", s.handleLikedContains)
	mux.HandleFunc("DELETE /me/library", s.handleRemoveFromLibrary)
	mux.HandleFunc("DELETE /playlists/{id}/followers", s.handleUnfollowPlaylist)
	mux.HandleFunc("GET /artists", s.handleArtists)

	s.srv = httptest.NewServer(s.record(mux))
	t.Cleanup(s.srv.Close)

	return s
}

// Client returns a spotify.Client that talks to the fake server.
func (s *Server) Client() *spotify.Client {
	return spotify.New(s.srv.Client(), spotify.WithBaseURL(s.srv.URL+"/"))
}

// AddPlaylist registers a playlist and its tracks. An empty ID is assigned.
// Returns the playlist ID.
func (s *Server) AddPlaylist(p Playlist) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p.ID == "" {
		p.ID = s.newID("pl")
	}
	for _, track := range p.Tracks {
		s.catalog[track.ID] = track
	}

	p.Tracks = slices.Clone(p.Tracks)
	s.playlists = append(s.playlists, &p)

	return p.ID
}

// AddCatalogTracks makes tracks known to the server without placing them
// anywhere, so they can be saved or added by ID.
func (s *Server) AddCatalogTracks(tracks ...Track) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, track := range tracks {
		s.catalog[track.ID] = track
	}
}

// Like saves tracks to Liked Songs at the given time, as the newest entries.
func (s *Server) Like(addedAt time.Time, tracks ...Track) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.like(addedAt, tracks)
}

// Unlike removes tracks from Liked Songs.
func (s *Server) Unlike(ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.liked = slices.DeleteFunc(s.liked, func(t SavedTrack) bool {
		return slices.Contains(ids, t.ID)
	})
}

// SetNow sets the timestamp used for tracks saved through the API.
func (s *Server) SetNow(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.now = now
}

// SetArtistGenres sets the genres returned for an artist.
func (s *Server) SetArtistGenres(artistID string, genres ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.artistGenres[artistID] = genres
}

// SetStatus makes every request matching "METHOD /path" fail with status.
// A status of 0 clears the override, including one set by FailAfter.
func (s *Server) SetStatus(methodAndPath string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if status == 0 {
		delete(s.statuses, methodAndPath)
		delete(s.failAfter, methodAndPath)
		return
	}
	s.statuses[methodAndPath] = status
}

// FailAfter lets the next okCalls requests matching "METHOD /path" succeed and
// fails every later one with status, for testing partial failures. It replaces
// an earlier FailAfter for the same request; SetStatus(methodAndPath, 0)
// clears it.
func (s *Server) FailAfter(methodAndPath string, okCalls, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.failAfter[methodAndPath] = failAfterRule{okCalls: okCalls, status: status}
}

// DeletePlaylist removes a playlist, as if the user deleted it in Spotify.
// Later requests for its ID fail with 404.
func (s *Server) DeletePlaylist(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.playlists = slices.DeleteFunc(s.playlists, func(p *Playlist) bool { return p.ID == id })
}

// LikedIDs returns the IDs of liked tracks, newest first.
func (s *Server) LikedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make([]string, len(s.liked))
	for i, track := range s.liked {
		ids[i] = track.ID
	}
	return ids
}

// PlaylistByName returns a copy of the first playlist with the given name.
func (s *Server) PlaylistByName(name string) (Playlist, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range s.playlists {
		if p.Name == name {
			return clonePlaylist(p), true
		}
	}
	return Playlist{}, false
}

// PlaylistTrackIDs returns the track IDs of a playlist in order.
func (s *Server) PlaylistTrackIDs(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	p := s.playlist(id)
	if p == nil {
		return nil
	}

	ids := make([]string, len(p.Tracks))
	for i, track := range p.Tracks {
		ids[i] = track.ID
	}
	return ids
}

// Requests returns every request received, as "METHOD /path".
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.requests)
}

// CountRequests returns how many requests matched "METHOD /path" exactly.
func (s *Server) CountRequests(methodAndPath string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for _, r := range s.requests {
		if r == methodAndPath {
			count++
		}
	}
	return count
}

// ResetRequests clears the request log.
func (s *Server) ResetRequests() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests = nil
}

func (s *Server) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path

		s.mu.Lock()
		s.requests = append(s.requests, key)
		status, fail := s.statuses[key]
		if rule, ok := s.failAfter[key]; ok && !fail {
			if rule.okCalls > 0 {
				rule.okCalls--
				s.failAfter[key] = rule
			} else {
				status, fail = rule.status, true
			}
		}
		s.mu.Unlock()

		if fail {
			writeError(w, status, "injected failure")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Wire types: the subset of Spotify's JSON the syncer reads.

type wireArtist struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Genres []string `json:"genres"`
}

type wireTrack struct {
	Type        string            `json:"type"`
	ID          string            `json:"id"`
	URI         string            `json:"uri"`
	Artists     []wireArtist      `json:"artists"`
	ExternalIDs map[string]string `json:"external_ids,omitempty"`
	IsPlayable  bool              `json:"is_playable"`
}

type wireSavedTrack struct {
	AddedAt string    `json:"added_at"`
	Track   wireTrack `json:"track"`
}

type wirePlaylistItem struct {
	Item  *wireTrack `json:"item,omitempty"`
	Track *wireTrack `json:"track"`
}

type wireSimplePlaylist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type pageItem interface {
	wireSavedTrack | wirePlaylistItem | wireSimplePlaylist
}

type wirePage[T pageItem] struct {
	Href   string `json:"href"`
	Items  []T    `json:"items"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
	Total  int    `json:"total"`
	Next   string `json:"next"`
}

type wireError struct {
	Error wireErrorBody `json:"error"`
}

type wireErrorBody struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

type wireUser struct {
	ID string `json:"id"`
}

type wireSnapshot struct {
	SnapshotID string `json:"snapshot_id"`
}

type wireArtists struct {
	Artists []*wireArtist `json:"artists"`
}

type addItemsRequest struct {
	URIs []string `json:"uris"`
}

type removeItemsRequest struct {
	Tracks []uriRef `json:"tracks"`
	Items  []uriRef `json:"items"`
}

type uriRef struct {
	URI string `json:"uri"`
}

type createPlaylistRequest struct {
	Name        string `json:"name"`
	Public      bool   `json:"public"`
	Description string `json:"description"`
}

func (s *Server) handleMe(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, wireUser{ID: s.userID})
}

func (s *Server) handleLikedTracks(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	items := make([]wireSavedTrack, len(s.liked))
	for i, saved := range s.liked {
		items[i] = wireSavedTrack{
			AddedAt: saved.AddedAt.UTC().Format(time.RFC3339),
			Track:   toWireTrack(saved.Track),
		}
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, paginate(s.srv.URL, r, items, maxLikedPageSize))
}

func (s *Server) handleLikedContains(w http.ResponseWriter, r *http.Request) {
	ids := requestTrackIDs(r)

	s.mu.Lock()
	result := make([]bool, len(ids))
	for i, id := range ids {
		result[i] = slices.ContainsFunc(s.liked, func(t SavedTrack) bool { return t.ID == id })
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleSaveTracks(w http.ResponseWriter, r *http.Request) {
	ids := requestTrackIDs(r)

	s.mu.Lock()
	defer s.mu.Unlock()

	tracks := make([]Track, 0, len(ids))
	for _, id := range ids {
		track, ok := s.catalog[id]
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		tracks = append(tracks, track)
	}

	s.like(s.now, tracks)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleMyPlaylists(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	items := make([]wireSimplePlaylist, len(s.playlists))
	for i, p := range s.playlists {
		items[i] = wireSimplePlaylist{ID: p.ID, Name: p.Name}
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, paginate(s.srv.URL, r, items, maxPlaylistPageSize))
}

func (s *Server) handleCreatePlaylist(w http.ResponseWriter, r *http.Request) {
	var req createPlaylistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.mu.Lock()
	p := &Playlist{ID: s.newID("pl"), Name: req.Name, Description: req.Description, Public: req.Public}
	s.playlists = append(s.playlists, p)
	s.mu.Unlock()

	writeJSON(w, http.StatusCreated, wireSimplePlaylist{ID: p.ID, Name: p.Name})
}

func (s *Server) handleGetPlaylist(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	p := s.playlist(r.PathValue("id"))
	s.mu.Unlock()

	if p == nil {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}

	writeJSON(w, http.StatusOK, wireSimplePlaylist{ID: p.ID, Name: p.Name})
}

func (s *Server) handlePlaylistItems(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	p := s.playlist(r.PathValue("id"))
	var items []wirePlaylistItem
	if p != nil {
		newShape := strings.HasSuffix(r.URL.Path, "/items")
		items = make([]wirePlaylistItem, len(p.Tracks))
		for i, track := range p.Tracks {
			wt := toWireTrack(track)
			items[i] = wirePlaylistItem{Track: &wt}
			if newShape {
				items[i].Item = &wt
			}
		}
	}
	s.mu.Unlock()

	if p == nil {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}

	maxLimit := maxItemsPageSize
	if strings.HasSuffix(r.URL.Path, "/items") {
		maxLimit = maxPlaylistPageSize
	}

	writeJSON(w, http.StatusOK, paginate(s.srv.URL, r, items, maxLimit))
}

func (s *Server) handleAddPlaylistItems(w http.ResponseWriter, r *http.Request) {
	var req addItemsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p := s.playlist(r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}

	for _, uri := range req.URIs {
		track, ok := s.catalog[strings.TrimPrefix(uri, trackURIPrefix)]
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		p.Tracks = append(p.Tracks, track)
	}

	writeJSON(w, http.StatusCreated, wireSnapshot{SnapshotID: s.newID("snap")})
}

func (s *Server) handleRemovePlaylistItems(w http.ResponseWriter, r *http.Request) {
	var req removeItemsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p := s.playlist(r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}

	remove := make(map[string]struct{}, len(req.Tracks)+len(req.Items))
	for _, ref := range slices.Concat(req.Tracks, req.Items) {
		remove[strings.TrimPrefix(ref.URI, trackURIPrefix)] = struct{}{}
	}
	p.Tracks = slices.DeleteFunc(p.Tracks, func(t Track) bool {
		_, ok := remove[t.ID]
		return ok
	})

	writeJSON(w, http.StatusOK, wireSnapshot{SnapshotID: s.newID("snap")})
}

func (s *Server) handleUnfollowPlaylist(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := r.PathValue("id")
	s.playlists = slices.DeleteFunc(s.playlists, func(p *Playlist) bool { return p.ID == id })
	w.WriteHeader(http.StatusOK)
}

// handleRemoveFromLibrary handles DELETE /me/library: track URIs are unliked
// and playlist URIs are unfollowed.
func (s *Server) handleRemoveFromLibrary(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for uri := range strings.SplitSeq(r.URL.Query().Get("uris"), ",") {
		if id, ok := strings.CutPrefix(uri, playlistURIPrefix); ok {
			s.playlists = slices.DeleteFunc(s.playlists, func(p *Playlist) bool { return p.ID == id })
			continue
		}

		id := strings.TrimPrefix(uri, trackURIPrefix)
		s.liked = slices.DeleteFunc(s.liked, func(t SavedTrack) bool { return t.ID == id })
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleArtists(w http.ResponseWriter, r *http.Request) {
	ids := splitIDs(r.URL.Query().Get("ids"))

	s.mu.Lock()
	result := wireArtists{Artists: make([]*wireArtist, len(ids))}
	for i, id := range ids {
		genres, ok := s.artistGenres[id]
		if !ok {
			continue
		}
		result.Artists[i] = &wireArtist{ID: id, Name: id, Genres: genres}
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, result)
}

// ownedOnly wraps a /playlists/{id}/items handler so that it fails with 403
// for playlists the fake user does not own, like the real API.
func (s *Server) ownedOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		p := s.playlist(r.PathValue("id"))
		owned := p == nil || p.Owner == "" || p.Owner == s.userID
		s.mu.Unlock()

		if !owned {
			writeError(w, http.StatusForbidden, "Forbidden.")
			return
		}

		next(w, r)
	}
}

// requestTrackIDs returns the track IDs of a library request, given either as
// ids=a,b (legacy /me/tracks) or uris=spotify:track:a,... (/me/library).
func requestTrackIDs(r *http.Request) []string {
	query := r.URL.Query()
	if raw := query.Get("uris"); raw != "" {
		uris := splitIDs(raw)
		ids := make([]string, len(uris))
		for i, uri := range uris {
			ids[i] = strings.TrimPrefix(uri, trackURIPrefix)
		}
		return ids
	}
	return splitIDs(query.Get("ids"))
}

// like must be called with s.mu held.
func (s *Server) like(addedAt time.Time, tracks []Track) {
	for _, track := range tracks {
		s.catalog[track.ID] = track
		if slices.ContainsFunc(s.liked, func(t SavedTrack) bool { return t.ID == track.ID }) {
			continue
		}
		s.liked = slices.Insert(s.liked, 0, SavedTrack{Track: track, AddedAt: addedAt})
	}
}

// playlist must be called with s.mu held.
func (s *Server) playlist(id string) *Playlist {
	for _, p := range s.playlists {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// newID must be called with s.mu held.
func (s *Server) newID(prefix string) string {
	s.nextID++
	return fmt.Sprintf("%s%d", prefix, s.nextID)
}

func clonePlaylist(p *Playlist) Playlist {
	c := *p
	c.Tracks = slices.Clone(p.Tracks)
	return c
}

func toWireTrack(t Track) wireTrack {
	artists := make([]wireArtist, len(t.ArtistIDs))
	for i, id := range t.ArtistIDs {
		artists[i] = wireArtist{ID: id, Name: id}
	}

	wt := wireTrack{
		Type:       "track",
		ID:         t.ID,
		URI:        trackURIPrefix + t.ID,
		Artists:    artists,
		IsPlayable: true,
	}
	if t.ISRC != "" {
		wt.ExternalIDs = map[string]string{"isrc": t.ISRC}
	}
	return wt
}

// paginate slices items by the request's limit/offset and builds an absolute
// next URL that keeps the request's other query parameters.
func paginate[T pageItem](baseURL string, r *http.Request, items []T, maxLimit int) wirePage[T] {
	query := r.URL.Query()

	limit, err := strconv.Atoi(query.Get("limit"))
	if err != nil || limit <= 0 || limit > maxLimit {
		limit = min(defaultPageSize, maxLimit)
	}

	offset, err := strconv.Atoi(query.Get("offset"))
	if err != nil || offset < 0 {
		offset = 0
	}

	start := min(offset, len(items))
	end := min(offset+limit, len(items))

	page := wirePage[T]{
		Href:   baseURL + r.URL.RequestURI(),
		Items:  items[start:end],
		Limit:  limit,
		Offset: offset,
		Total:  len(items),
	}

	if end < len(items) {
		next := url.Values{}
		maps.Copy(next, query)
		next.Set("limit", strconv.Itoa(limit))
		next.Set("offset", strconv.Itoa(end))
		page.Next = baseURL + r.URL.Path + "?" + next.Encode()
	}

	return page
}

func splitIDs(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

func writeJSON[T wireUser | wireSnapshot | wireArtists | wireSimplePlaylist | []bool |
	wirePage[wireSavedTrack] | wirePage[wirePlaylistItem] | wirePage[wireSimplePlaylist]](
	w http.ResponseWriter, status int, body T,
) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(wireError{Error: wireErrorBody{Status: status, Message: message}})
}

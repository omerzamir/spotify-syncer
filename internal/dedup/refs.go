package dedup

import (
	"github.com/omerzamir/spotify/v2"
)

// TrackRef identifies a track by Spotify ID and, when known, its ISRC. The
// same recording released as a single and on an album has two IDs but one
// ISRC.
type TrackRef struct {
	ID   spotify.ID
	ISRC string
}

// UniqueTracks removes duplicate tracks while preserving the order of first
// occurrence. Two tracks are duplicates when they share a Spotify ID or a
// non-empty ISRC; tracks without an ISRC are only matched by ID. Entries with
// an empty ID are dropped and not counted. Returns the kept tracks and how
// many duplicates were skipped.
func UniqueTracks(tracks []TrackRef) ([]TrackRef, int) {
	seenIDs := make(map[spotify.ID]struct{}, len(tracks))
	seenISRCs := make(map[string]struct{}, len(tracks))
	unique := make([]TrackRef, 0, len(tracks))
	skipped := 0

	for _, track := range tracks {
		if track.ID == "" {
			continue
		}

		if _, ok := seenIDs[track.ID]; ok {
			skipped++
			continue
		}

		if track.ISRC != "" {
			if _, ok := seenISRCs[track.ISRC]; ok {
				skipped++
				continue
			}
			seenISRCs[track.ISRC] = struct{}{}
		}

		seenIDs[track.ID] = struct{}{}
		unique = append(unique, track)
	}

	return unique, skipped
}

// TrackIDs returns the IDs of tracks in order.
func TrackIDs(tracks []TrackRef) []spotify.ID {
	ids := make([]spotify.ID, len(tracks))
	for i, track := range tracks {
		ids[i] = track.ID
	}
	return ids
}

// LikedIndex looks up which liked tracks match a given track, either by exact
// ID or by sharing its ISRC. It is built from liked-track data and does not
// call the API.
type LikedIndex struct {
	byID   map[spotify.ID]struct{}
	byISRC map[string][]spotify.ID
}

// NewLikedIndex returns an empty LikedIndex.
func NewLikedIndex() *LikedIndex {
	return &LikedIndex{
		byID:   make(map[spotify.ID]struct{}),
		byISRC: make(map[string][]spotify.ID),
	}
}

// Add records a liked track. An empty isrc indexes the track by ID only.
func (x *LikedIndex) Add(id spotify.ID, isrc string) {
	if id == "" {
		return
	}

	if _, ok := x.byID[id]; ok {
		return
	}

	x.byID[id] = struct{}{}

	if isrc != "" {
		x.byISRC[isrc] = append(x.byISRC[isrc], id)
	}
}

// Len returns the number of indexed liked tracks.
func (x *LikedIndex) Len() int {
	return len(x.byID)
}

// Matches returns the liked track IDs that make t "already liked": t's own ID
// if it is liked, followed by every other liked track sharing t's non-empty
// ISRC, without duplicates. Returns nil when nothing matches.
//
// The ISRC siblings are returned even when t's own ID is liked, because a
// liked track from a possibly stale cache may no longer be liked while another
// release of the same recording still is: verifying only t's own ID would then
// wrongly report it as not liked.
func (x *LikedIndex) Matches(t TrackRef) []spotify.ID {
	var matches []spotify.ID

	if _, ok := x.byID[t.ID]; ok {
		matches = append(matches, t.ID)
	}

	if t.ISRC == "" {
		return matches
	}

	for _, id := range x.byISRC[t.ISRC] {
		if id != t.ID {
			matches = append(matches, id)
		}
	}

	return matches
}

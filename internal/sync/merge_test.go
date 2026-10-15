package sync

import (
	"testing"

	"github.com/zmb3/spotify/v2"
)

func TestMergeThreePlaylistsDeduplication(t *testing.T) {
	s1 := []spotify.ID{"trackA", "trackB", "trackC"}
	s2 := []spotify.ID{"trackB", "trackD", "trackA"}
	s3 := []spotify.ID{"trackE", "trackC", "trackF"}

	allPlaylists := [][]spotify.ID{s1, s2, s3}

	seen := make(map[spotify.ID]bool)
	var combined []spotify.ID

	for _, list := range allPlaylists {
		for _, id := range list {
			if !seen[id] {
				seen[id] = true
				combined = append(combined, id)
			}
		}
	}

	expected := []spotify.ID{"trackA", "trackB", "trackC", "trackD", "trackE", "trackF"}

	if len(combined) != len(expected) {
		t.Fatalf("expected len %d, got %d", len(expected), len(combined))
	}

	for i, exp := range expected {
		if combined[i] != exp {
			t.Errorf("at index %d: expected %s, got %s", i, exp, combined[i])
		}
	}
}

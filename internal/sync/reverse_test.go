package sync

import (
	"testing"

	"github.com/zmb3/spotify/v2"
)

func TestDeduplicateTrackIDs(t *testing.T) {
	input := []spotify.ID{
		"track1",
		"track2",
		"track1",
		"",
		"track3",
		"track2",
		"track4",
	}

	expected := []spotify.ID{
		"track1",
		"track2",
		"track3",
		"track4",
	}

	got := deduplicateTrackIDs(input)

	if len(got) != len(expected) {
		t.Fatalf("expected len %d, got %d", len(expected), len(got))
	}

	for i := range expected {
		if got[i] != expected[i] {
			t.Errorf("at index %d: expected %s, got %s", i, expected[i], got[i])
		}
	}
}

package sync

import (
	"slices"
	"testing"

	"github.com/omerzamir/spotify/v2"
)

func TestAppliedState(t *testing.T) {
	tests := []struct {
		name    string
		current []spotify.ID
		added   []spotify.ID
		removed []spotify.ID
		want    []spotify.ID
	}{
		{name: "nothing applied", current: []spotify.ID{"a", "b"}, want: []spotify.ID{"a", "b"}},
		{
			name:    "added tracks follow the current ones",
			current: []spotify.ID{"a", "b"},
			added:   []spotify.ID{"c", "d"},
			want:    []spotify.ID{"a", "b", "c", "d"},
		},
		{
			name:    "removed tracks are dropped in place",
			current: []spotify.ID{"a", "b", "c"},
			added:   []spotify.ID{"d"},
			removed: []spotify.ID{"b"},
			want:    []spotify.ID{"a", "c", "d"},
		},
		{name: "empty playlist", added: []spotify.ID{"a"}, want: []spotify.ID{"a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appliedState(tt.current, tt.added, tt.removed); !slices.Equal(got, tt.want) {
				t.Errorf("appliedState = %v, want %v", got, tt.want)
			}
		})
	}
}

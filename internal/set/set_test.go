package set_test

import (
	"slices"
	"testing"

	"spotify-syncer/internal/set"
)

func TestSetBasics(t *testing.T) {
	s := set.Of("a", "b", "a")

	if got := s.Len(); got != 2 {
		t.Errorf("Len() = %d, want 2", got)
	}
	if !s.Contains("a") || s.Contains("c") {
		t.Errorf("Contains: got a=%t c=%t, want a=true c=false", s.Contains("a"), s.Contains("c"))
	}
	if s.Add("a") {
		t.Error("Add(existing) = true, want false")
	}
	if !s.Add("c") {
		t.Error("Add(new) = false, want true")
	}
	if got, want := slices.Sorted(s.All()), []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("All() = %v, want %v", got, want)
	}
	if got := set.Collect(slices.Values([]int{3, 1, 3})); got.Len() != 2 {
		t.Errorf("Collect len = %d, want 2", got.Len())
	}
}

func TestMissing(t *testing.T) {
	tests := []struct {
		name    string
		xs      []string
		exclude set.Set[string]
		want    []string
	}{
		{name: "preserves order of xs", xs: []string{"c", "a", "b"}, exclude: set.Of("a"), want: []string{"c", "b"}},
		{
			name:    "drops duplicates in xs",
			xs:      []string{"a", "b", "a"},
			exclude: set.Of[string](),
			want:    []string{"a", "b"},
		},
		{name: "all excluded", xs: []string{"a"}, exclude: set.Of("a"), want: nil},
		{name: "empty input", xs: nil, exclude: set.Of("a"), want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := set.Missing(tt.xs, tt.exclude); !slices.Equal(got, tt.want) {
				t.Errorf("Missing(%v) = %v, want %v", tt.xs, got, tt.want)
			}
		})
	}
}

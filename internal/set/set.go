// Package set provides a minimal generic set backed by a map, with
// order-preserving helpers for diffing slices.
package set

import (
	"iter"
	"maps"
)

// Set is an unordered collection of unique comparable values. The zero value
// is not usable; create sets with New, Of or Collect. A Set is not safe for
// concurrent use.
type Set[T comparable] map[T]struct{}

// New returns an empty set with room for size elements.
func New[T comparable](size int) Set[T] {
	return make(Set[T], size)
}

// Of returns a set containing items.
func Of[T comparable](items ...T) Set[T] {
	s := New[T](len(items))
	for _, item := range items {
		s.Add(item)
	}
	return s
}

// Collect returns a set containing every value yielded by seq.
func Collect[T comparable](seq iter.Seq[T]) Set[T] {
	s := New[T](0)
	for item := range seq {
		s.Add(item)
	}
	return s
}

// Add inserts v and reports whether it was newly added.
func (s Set[T]) Add(v T) bool {
	if s.Contains(v) {
		return false
	}
	s[v] = struct{}{}
	return true
}

// Contains reports whether v is in the set.
func (s Set[T]) Contains(v T) bool {
	_, ok := s[v]
	return ok
}

// Len returns the number of elements.
func (s Set[T]) Len() int {
	return len(s)
}

// All returns an iterator over the elements in unspecified order.
func (s Set[T]) All() iter.Seq[T] {
	return maps.Keys(s)
}

// Missing returns the unique items of xs that are not in exclude, in the
// order they first appear in xs. It is an order-preserving set difference.
func Missing[T comparable](xs []T, exclude Set[T]) []T {
	seen := New[T](len(xs))
	var out []T
	for _, x := range xs {
		if exclude.Contains(x) || !seen.Add(x) {
			continue
		}
		out = append(out, x)
	}
	return out
}

package main

import (
	"encoding/json"
	"fmt"
	"io"

	"spotify-syncer/internal/sync"
)

// syncSummary is the JSON summary printed to stdout after a sync run. Bucket
// counts are pointers so they are omitted when genre sync is disabled but
// still printed as 0 when it is enabled.
type syncSummary struct {
	Success        bool     `json:"success"`
	LikedTotal     int      `json:"liked_total"`
	DedupRemoved   int      `json:"dedup_removed"`
	MirrorAdded    int      `json:"mirror_added"`
	MirrorRemoved  int      `json:"mirror_removed"`
	DurationSec    float64  `json:"duration_sec"`
	BucketsCreated *int     `json:"buckets_created,omitempty"`
	BucketsRemoved *int     `json:"buckets_removed,omitempty"`
	DuplicateIDs   []string `json:"duplicate_ids,omitempty"`
}

// newSyncSummary builds the summary for a finished sync run.
func newSyncSummary(result sync.RunResult, genresEnabled bool) syncSummary {
	m := result.Metrics

	summary := syncSummary{
		Success:       result.Success,
		LikedTotal:    m.LikedTotal,
		DedupRemoved:  m.DedupRemoved,
		MirrorAdded:   m.MirrorAdded,
		MirrorRemoved: m.MirrorRemoved,
		DurationSec:   m.FinishedAt.Sub(m.StartedAt).Seconds(),
		DuplicateIDs:  m.DuplicateIDs,
	}

	if genresEnabled {
		summary.BucketsCreated = new(m.BucketsCreated)
		summary.BucketsRemoved = new(m.BucketsRemoved)
	}

	return summary
}

// reverseSummary is the JSON summary printed to stdout after a reverse sync.
type reverseSummary struct {
	sync.ReverseResult

	Success bool `json:"success"`
}

// mergeSummary is the JSON summary printed to stdout after a merge.
type mergeSummary struct {
	sync.MergeResult

	Success bool `json:"success"`
}

// writeSummary writes summary to w as a single line of JSON.
func writeSummary[T syncSummary | reverseSummary | mergeSummary](w io.Writer, summary T) error {
	if err := json.NewEncoder(w).Encode(summary); err != nil {
		return fmt.Errorf("writing summary: %w", err)
	}
	return nil
}

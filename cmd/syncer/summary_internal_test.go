package main

import (
	"encoding/json"
	"testing"
	"time"

	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/sync"
)

func TestSyncSummaryJSON(t *testing.T) {
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		metrics       cache.RunMetrics
		genresEnabled bool
		want          string
	}{
		{
			name: "full mode with genres disabled omits bucket counts and new tracks",
			metrics: cache.RunMetrics{
				Mode:       "full",
				LikedTotal: 3,
				StartedAt:  started,
				FinishedAt: started.Add(2 * time.Second),
			},
			want: `{"success":true,"mode":"full","liked_total":3,"dedup_removed":0,` +
				`"mirror_added":0,"mirror_removed":0,"duration_sec":2}`,
		},
		{
			name:          "genres enabled keeps zero bucket counts",
			metrics:       cache.RunMetrics{Mode: "full"},
			genresEnabled: true,
			want: `{"success":true,"mode":"full","liked_total":0,"dedup_removed":0,` +
				`"mirror_added":0,"mirror_removed":0,"duration_sec":0,"buckets_created":0,"buckets_removed":0}`,
		},
		{
			name:    "fast mode reports new tracks even when zero",
			metrics: cache.RunMetrics{Mode: "fast", LikedTotal: 10},
			want: `{"success":true,"mode":"fast","liked_total":10,"new_tracks":0,"dedup_removed":0,` +
				`"mirror_added":0,"mirror_removed":0,"duration_sec":0}`,
		},
		{
			name:    "duplicate ids included when present",
			metrics: cache.RunMetrics{Mode: "full", DedupRemoved: 1, DuplicateIDs: []string{"a"}},
			want: `{"success":true,"mode":"full","liked_total":0,"dedup_removed":1,` +
				`"mirror_added":0,"mirror_removed":0,"duration_sec":0,"duplicate_ids":["a"]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := newSyncSummary(sync.RunResult{Metrics: tt.metrics, Success: true}, tt.genresEnabled)

			got, err := json.Marshal(summary)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestReverseSummaryJSONFlattensResult(t *testing.T) {
	got, err := json.Marshal(reverseSummary{
		Success:      true,
		PlaylistName: "p", PlaylistID: "id", TotalTracks: 2, NewlyAdded: 1,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"playlist_name":"p","playlist_id":"id","total_tracks":2,"duplicates_skipped":0,"already_liked":0,"newly_added":1,"dry_run":false,"duration_sec":0,"success":true}`
	if string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestMergeSummaryJSONOmitsEmptyTargetID(t *testing.T) {
	got, err := json.Marshal(mergeSummary{
		Success:    true,
		TargetName: "t", DryRun: true,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"sources":null,"target_name":"t","total_tracks":0,"unique_tracks":0,"duplicates_skipped":0,"dry_run":true,"duration_sec":0,"success":true}`
	if string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

package main

import (
	"encoding/json"
	"testing"

	"spotify-syncer/internal/sync"
)

func TestSyncSummaryJSON(t *testing.T) {
	zero := 0

	tests := []struct {
		name    string
		summary syncSummary
		want    string
	}{
		{
			name:    "genres disabled omits bucket counts",
			summary: syncSummary{Success: true, LikedTotal: 3},
			want:    `{"success":true,"liked_total":3,"dedup_removed":0,"mirror_added":0,"mirror_removed":0,"duration_sec":0}`,
		},
		{
			name:    "genres enabled keeps zero bucket counts",
			summary: syncSummary{Success: true, BucketsCreated: &zero, BucketsRemoved: &zero},
			want:    `{"success":true,"liked_total":0,"dedup_removed":0,"mirror_added":0,"mirror_removed":0,"duration_sec":0,"buckets_created":0,"buckets_removed":0}`,
		},
		{
			name:    "duplicate ids included when present",
			summary: syncSummary{DuplicateIDs: []string{"a"}},
			want:    `{"success":false,"liked_total":0,"dedup_removed":0,"mirror_added":0,"mirror_removed":0,"duration_sec":0,"duplicate_ids":["a"]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.summary)
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
		Success:       true,
		ReverseResult: sync.ReverseResult{PlaylistName: "p", PlaylistID: "id", TotalTracks: 2, NewlyAdded: 1},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"success":true,"playlist_name":"p","playlist_id":"id","total_tracks":2,"already_liked":0,"newly_added":1,"dry_run":false,"duration_sec":0}`
	if string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestMergeSummaryJSONOmitsEmptyTargetID(t *testing.T) {
	got, err := json.Marshal(mergeSummary{
		Success:     true,
		MergeResult: sync.MergeResult{TargetName: "t", DryRun: true},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"success":true,"sources":null,"target_name":"t","total_tracks":0,"unique_tracks":0,"duplicates_skipped":0,"dry_run":true,"duration_sec":0}`
	if string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

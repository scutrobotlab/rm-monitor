package highlight

import (
	"testing"

	common "scutbot.cn/web/rm-monitor/pkg/config"
)

func TestFindCandidatesDetectsAndMergesPeaks(t *testing.T) {
	stats := DanmuStats{BucketSeconds: 10, Points: []DanmuPoint{
		{T: 0, Count: 1}, {T: 10, Count: 1}, {T: 20, Count: 1},
		{T: 30, Count: 8}, {T: 40, Count: 1}, {T: 50, Count: 1},
		{T: 60, Count: 1}, {T: 70, Count: 1}, {T: 80, Count: 1}, {T: 90, Count: 1}, {T: 100, Count: 1},
		{T: 110, Count: 9},
	}}
	got := FindCandidates(stats, OnlineStats{}, common.HighlightConf{
		MaxHighlightsPerRound: 5,
		MinClipSeconds:        20,
		MaxClipSeconds:        90,
		PreSeconds:            10,
		PostSeconds:           20,
		MergeGapSeconds:       20,
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %d: %+v", len(got), got)
	}
	if got[0].Peak != 30 {
		t.Fatalf("first peak = %v", got[0].Peak)
	}
	if got[1].Peak != 110 {
		t.Fatalf("second peak = %v", got[1].Peak)
	}
}

func TestFindCandidatesLimitsCount(t *testing.T) {
	stats := DanmuStats{BucketSeconds: 10, Points: []DanmuPoint{
		{T: 0, Count: 1}, {T: 10, Count: 5}, {T: 20, Count: 1}, {T: 30, Count: 1},
		{T: 40, Count: 1}, {T: 50, Count: 1}, {T: 60, Count: 6}, {T: 70, Count: 1},
		{T: 80, Count: 1}, {T: 90, Count: 1}, {T: 100, Count: 1}, {T: 110, Count: 1}, {T: 120, Count: 7},
	}}
	got := FindCandidates(stats, OnlineStats{}, common.HighlightConf{
		MaxHighlightsPerRound: 2,
		MinClipSeconds:        20,
		MaxClipSeconds:        40,
		PreSeconds:            5,
		PostSeconds:           15,
		MergeGapSeconds:       5,
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(got))
	}
	for i, c := range got {
		if c.Index != i+1 {
			t.Fatalf("candidate index = %d at %d", c.Index, i)
		}
		if c.End-c.Start > 40 {
			t.Fatalf("candidate too long: %+v", c)
		}
	}
}

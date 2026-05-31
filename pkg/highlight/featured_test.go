package highlight

import "testing"

func TestSelectFeaturedKeepsAtLeastOnePerRoundAndLimitsTotal(t *testing.T) {
	candidates := []FeaturedCandidate{
		{RoundNo: 1, HighlightIndex: 1, Score: 1, Key: "r1-1"},
		{RoundNo: 1, HighlightIndex: 2, Score: 9, Key: "r1-2"},
		{RoundNo: 1, HighlightIndex: 3, Score: 8, Key: "r1-3"},
		{RoundNo: 2, HighlightIndex: 1, Score: 2, Key: "r2-1"},
		{RoundNo: 3, HighlightIndex: 1, Score: 7, Key: "r3-1"},
		{RoundNo: 3, HighlightIndex: 2, Score: 6, Key: "r3-2"},
	}
	got := SelectFeatured(candidates, 2, 4)
	want := []int{1, 2, 4, 3}
	if len(got) != len(want) {
		t.Fatalf("len=%d want=%d got=%#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got=%#v want=%#v", got, want)
		}
	}
}

func TestSelectFeaturedDedupesKey(t *testing.T) {
	candidates := []FeaturedCandidate{
		{RoundNo: 1, HighlightIndex: 1, Score: 10, Key: "same"},
		{RoundNo: 1, HighlightIndex: 2, Score: 9, Key: "same"},
		{RoundNo: 1, HighlightIndex: 3, Score: 8, Key: "other"},
	}
	got := SelectFeatured(candidates, 2, 9)
	want := []int{0, 2}
	if len(got) != len(want) {
		t.Fatalf("len=%d want=%d got=%#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got=%#v want=%#v", got, want)
		}
	}
}

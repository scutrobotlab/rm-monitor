package highlight

import "sort"

type FeaturedCandidate struct {
	RoundNo        int
	HighlightIndex int
	Score          float64
	Key            string
}

func SelectFeatured(candidates []FeaturedCandidate, perRoundLimit, totalLimit int) []int {
	if perRoundLimit <= 0 || totalLimit <= 0 || len(candidates) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(candidates))
	byRound := make(map[int][]int)
	rounds := make([]int, 0)
	roundSeen := make(map[int]struct{})
	for i, c := range candidates {
		if c.RoundNo <= 0 || c.Key == "" {
			continue
		}
		if _, ok := seen[c.Key]; ok {
			continue
		}
		seen[c.Key] = struct{}{}
		if _, ok := roundSeen[c.RoundNo]; !ok {
			roundSeen[c.RoundNo] = struct{}{}
			rounds = append(rounds, c.RoundNo)
		}
		byRound[c.RoundNo] = append(byRound[c.RoundNo], i)
	}
	sort.Ints(rounds)
	less := func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.RoundNo != b.RoundNo {
			return a.RoundNo < b.RoundNo
		}
		return a.HighlightIndex < b.HighlightIndex
	}
	mandatory := make([]int, 0, len(rounds))
	extras := make([]int, 0)
	for _, roundNo := range rounds {
		idxs := byRound[roundNo]
		sort.SliceStable(idxs, func(i, j int) bool { return less(idxs[i], idxs[j]) })
		if len(idxs) == 0 {
			continue
		}
		mandatory = append(mandatory, idxs[0])
		limit := perRoundLimit
		if len(idxs) < limit {
			limit = len(idxs)
		}
		extras = append(extras, idxs[1:limit]...)
	}
	sort.SliceStable(mandatory, func(i, j int) bool { return less(mandatory[i], mandatory[j]) })
	if len(mandatory) >= totalLimit {
		return append([]int(nil), mandatory[:totalLimit]...)
	}
	sort.SliceStable(extras, func(i, j int) bool { return less(extras[i], extras[j]) })
	out := append([]int(nil), mandatory...)
	for _, idx := range extras {
		if len(out) >= totalLimit {
			break
		}
		out = append(out, idx)
	}
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

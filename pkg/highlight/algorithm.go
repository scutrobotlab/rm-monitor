package highlight

import (
	"encoding/json"
	"math"
	"os"
	"sort"

	common "scutbot.cn/web/rm-monitor/pkg/config"
)

const rollingWindowBuckets = 6

type DanmuStats struct {
	BucketSeconds int          `json:"bucket_seconds"`
	Points        []DanmuPoint `json:"points"`
}

type DanmuPoint struct {
	T     float64 `json:"t"`
	Count int     `json:"count"`
	Total int     `json:"total"`
}

type OnlineStats struct {
	Points []OnlinePoint `json:"points"`
}

type OnlinePoint struct {
	T           float64  `json:"t"`
	OnlineCount *float64 `json:"online_count"`
}

type Candidate struct {
	Index int
	Start float64
	End   float64
	Peak  float64
	Score float64
}

func LoadDanmuStats(path string) (DanmuStats, error) {
	var stats DanmuStats
	raw, err := os.ReadFile(path)
	if err != nil {
		return stats, err
	}
	if err := json.Unmarshal(raw, &stats); err != nil {
		return stats, err
	}
	return stats, nil
}

func LoadOnlineStats(path string) (OnlineStats, error) {
	var stats OnlineStats
	raw, err := os.ReadFile(path)
	if err != nil {
		return stats, err
	}
	if err := json.Unmarshal(raw, &stats); err != nil {
		return stats, err
	}
	return stats, nil
}

func FindCandidates(stats DanmuStats, online OnlineStats, conf common.HighlightConf) []Candidate {
	conf = conf.WithDefaults()
	points := normalizeBuckets(stats)
	sort.Slice(points, func(i, j int) bool { return points[i].T < points[j].T })
	onlineByT := make(map[float64]float64)
	for _, p := range online.Points {
		if p.OnlineCount != nil {
			onlineByT[p.T] = *p.OnlineCount
		}
	}

	var raw []Candidate
	for i, p := range points {
		mean, std := rollingStats(points, i)
		threshold := math.Max(3, mean+2*std)
		if float64(p.Count) < threshold {
			continue
		}
		z := float64(p.Count) - threshold
		if std > 0 {
			z = (float64(p.Count) - mean) / std
		}
		score := z
		if onlineCount, ok := onlineByT[p.T]; ok && onlineCount > 0 {
			score += math.Log10(onlineCount+1) * 0.1
		}
		raw = append(raw, Candidate{
			Start: math.Max(0, p.T-float64(conf.PreSeconds)),
			End:   p.T + float64(conf.PostSeconds),
			Peak:  p.T,
			Score: score,
		})
	}
	if len(raw) == 0 {
		return nil
	}
	sort.Slice(raw, func(i, j int) bool { return raw[i].Start < raw[j].Start })
	merged := mergeCandidates(raw, conf)
	for i := range merged {
		merged[i] = enforceLength(merged[i], conf)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Score > merged[j].Score })
	if max := conf.MaxHighlightsPerRound; max > 0 && len(merged) > max {
		merged = merged[:max]
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Start < merged[j].Start })
	for i := range merged {
		merged[i].Index = i + 1
	}
	return merged
}

func normalizeBuckets(stats DanmuStats) []DanmuPoint {
	points := append([]DanmuPoint(nil), stats.Points...)
	sort.Slice(points, func(i, j int) bool { return points[i].T < points[j].T })
	if len(points) == 0 {
		return nil
	}
	step := float64(stats.BucketSeconds)
	if step <= 0 {
		step = 10
	}
	byT := make(map[float64]DanmuPoint, len(points))
	for _, p := range points {
		byT[p.T] = p
	}
	start := math.Floor(points[0].T/step) * step
	end := math.Floor(points[len(points)-1].T/step) * step
	out := make([]DanmuPoint, 0, int((end-start)/step)+1)
	var total int
	for t := start; t <= end; t += step {
		p, ok := byT[t]
		if ok {
			if p.Total > 0 {
				total = p.Total
			} else {
				total += p.Count
				p.Total = total
			}
			out = append(out, p)
			continue
		}
		out = append(out, DanmuPoint{T: t, Count: 0, Total: total})
	}
	return out
}

func rollingStats(points []DanmuPoint, i int) (float64, float64) {
	start := i - rollingWindowBuckets
	if start < 0 {
		start = 0
	}
	if start == i {
		return 0, 0
	}
	var sum float64
	for _, p := range points[start:i] {
		sum += float64(p.Count)
	}
	mean := sum / float64(i-start)
	var sq float64
	for _, p := range points[start:i] {
		d := float64(p.Count) - mean
		sq += d * d
	}
	return mean, math.Sqrt(sq / float64(i-start))
}

func mergeCandidates(candidates []Candidate, conf common.HighlightConf) []Candidate {
	merged := []Candidate{candidates[0]}
	for _, c := range candidates[1:] {
		last := &merged[len(merged)-1]
		if c.Start-last.End <= float64(conf.MergeGapSeconds) {
			if c.End > last.End {
				last.End = c.End
			}
			if c.Score > last.Score {
				last.Score = c.Score
				last.Peak = c.Peak
			}
			continue
		}
		merged = append(merged, c)
	}
	return merged
}

func enforceLength(c Candidate, conf common.HighlightConf) Candidate {
	minLen := float64(conf.MinClipSeconds)
	maxLen := float64(conf.MaxClipSeconds)
	length := c.End - c.Start
	if length < minLen {
		need := minLen - length
		c.Start = math.Max(0, c.Start-need/2)
		c.End += need / 2
	}
	length = c.End - c.Start
	if length > maxLen {
		half := maxLen / 2
		c.Start = math.Max(0, c.Peak-half)
		c.End = c.Start + maxLen
	}
	return c
}

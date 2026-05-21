package subtitle

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

var ErrNoCues = errors.New("no subtitle cues")

type Options struct {
	Start *float64
	End   *float64
}

type sttRow struct {
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
	Status string  `json:"status"`
	Text   string  `json:"text"`
}

type cue struct {
	Start float64
	End   float64
	Text  string
}

func WriteSRTFromJSONL(input, output string, opts Options) error {
	cues, err := readCues(input, opts)
	if err != nil {
		return err
	}
	if len(cues) == 0 {
		return ErrNoCues
	}
	return writeSRT(output, cues)
}

func readCues(path string, opts Options) ([]cue, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var start, end float64
	cropped := opts.Start != nil && opts.End != nil
	if cropped {
		start = *opts.Start
		end = *opts.End
		if end <= start {
			return nil, fmt.Errorf("invalid subtitle crop %.3f-%.3f", start, end)
		}
	}

	var cues []cue
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row sttRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		text := strings.TrimSpace(row.Text)
		if row.Status != "SUCCEEDED" || text == "" || row.End <= row.Start {
			continue
		}
		cueStart := row.Start
		cueEnd := row.End
		if cropped {
			if cueEnd <= start || cueStart >= end {
				continue
			}
			cueStart = math.Max(cueStart, start) - start
			cueEnd = math.Min(cueEnd, end) - start
		}
		if cueEnd <= cueStart {
			continue
		}
		cues = append(cues, cue{Start: cueStart, End: cueEnd, Text: text})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return cues, nil
}

func writeSRT(path string, cues []cue) error {
	tmp := path + ".part"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	for i, cue := range cues {
		if _, err := fmt.Fprintf(f, "%d\n%s --> %s\n%s\n\n", i+1, formatTime(cue.Start), formatTime(cue.End), normalizeText(cue.Text)); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func normalizeText(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\r", "\n")), " ")
}

func formatTime(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	totalMillis := int64(math.Round(seconds * 1000))
	ms := totalMillis % 1000
	totalSeconds := totalMillis / 1000
	sec := totalSeconds % 60
	totalMinutes := totalSeconds / 60
	min := totalMinutes % 60
	hour := totalMinutes / 60
	return fmt.Sprintf("%02d:%02d:%02d,%03d", hour, min, sec, ms)
}

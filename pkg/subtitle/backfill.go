package subtitle

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"

	common "scutbot.cn/web/rm-monitor/pkg/config"
)

type BackfillOptions struct {
	Force      bool
	ListOnly   bool
	Rounds     bool
	Highlights bool
	Limit      int
	Writer     io.Writer
}

type BackfillSummary struct {
	RoundGenerated              int
	RoundSkippedExisting        int
	RoundSkippedNoCues          int
	HighlightGenerated          int
	HighlightSkippedExisting    int
	HighlightSkippedMissingSTT  int
	HighlightSkippedInvalidMeta int
	HighlightSkippedNoCues      int
	Listed                      int
}

type highlightMeta struct {
	StartSeconds float64 `json:"start_seconds"`
	EndSeconds   float64 `json:"end_seconds"`
}

func Backfill(conf common.RecordConf, opts BackfillOptions) (BackfillSummary, error) {
	conf = conf.WithDefaults()
	baseDir := strings.TrimSpace(conf.BaseDir)
	if baseDir == "" {
		return BackfillSummary{}, errors.New("RecordConf.BaseDir is empty")
	}
	role := strings.TrimSpace(conf.STTRole)
	if role == "" && opts.Rounds {
		return BackfillSummary{}, errors.New("RecordConf.STTRole is empty")
	}
	if !opts.Rounds && !opts.Highlights {
		opts.Rounds = true
		opts.Highlights = true
	}

	var summary BackfillSummary
	err := filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if opts.Limit > 0 && generatedTotal(summary) >= opts.Limit {
			return filepath.SkipAll
		}
		switch filepath.Base(path) {
		case "stt.jsonl":
			if opts.Rounds {
				return backfillRoundSubtitle(path, role, opts, &summary)
			}
		case "highlight.json":
			if opts.Highlights {
				return backfillHighlightSubtitle(path, opts, &summary)
			}
		}
		return nil
	})
	if err != nil {
		return summary, err
	}
	return summary, nil
}

func generatedTotal(summary BackfillSummary) int {
	return summary.RoundGenerated + summary.HighlightGenerated
}

func backfillRoundSubtitle(sttPath, role string, opts BackfillOptions, summary *BackfillSummary) error {
	out := filepath.Join(filepath.Dir(sttPath), fmt.Sprintf("%s.srt", role))
	if !opts.Force && fileExists(out) {
		summary.RoundSkippedExisting++
		return nil
	}
	if opts.ListOnly {
		summary.Listed++
		writeLine(opts.Writer, "round %s -> %s", sttPath, out)
		return nil
	}
	err := WriteSRTFromJSONL(sttPath, out, Options{})
	if errors.Is(err, ErrNoCues) {
		summary.RoundSkippedNoCues++
		return nil
	}
	if err != nil {
		return errors.Wrapf(err, "write round subtitle %s", out)
	}
	summary.RoundGenerated++
	return nil
}

func backfillHighlightSubtitle(metaPath string, opts BackfillOptions, summary *BackfillSummary) error {
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return err
	}
	var meta highlightMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return errors.Wrapf(err, "parse highlight metadata %s", metaPath)
	}
	if meta.EndSeconds <= meta.StartSeconds {
		summary.HighlightSkippedInvalidMeta++
		return nil
	}
	outputDir := filepath.Dir(metaPath)
	out := filepath.Join(outputDir, "video.srt")
	if !opts.Force && fileExists(out) {
		summary.HighlightSkippedExisting++
		return nil
	}
	roundDir := filepath.Dir(filepath.Dir(outputDir))
	sttPath := filepath.Join(roundDir, "stt.jsonl")
	if !fileExists(sttPath) {
		summary.HighlightSkippedMissingSTT++
		return nil
	}
	if opts.ListOnly {
		summary.Listed++
		writeLine(opts.Writer, "highlight %s -> %s", metaPath, out)
		return nil
	}
	err = WriteSRTFromJSONL(sttPath, out, Options{
		Start: &meta.StartSeconds,
		End:   &meta.EndSeconds,
	})
	if errors.Is(err, ErrNoCues) {
		summary.HighlightSkippedNoCues++
		return nil
	}
	if err != nil {
		return errors.Wrapf(err, "write highlight subtitle %s", out)
	}
	summary.HighlightGenerated++
	return nil
}

func writeLine(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintf(w, format+"\n", args...)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

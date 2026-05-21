package main

import (
	"flag"
	"fmt"
	"os"

	"scutbot.cn/web/rm-monitor/pkg/app"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/subtitle"
)

type config struct {
	RecordConf common.RecordConf
}

var (
	configFile    = flag.String("f", "etc/config.yml", "the config file")
	force         = flag.Bool("force", false, "overwrite existing subtitle files")
	listOnly      = flag.Bool("list", false, "list subtitle backfill candidates without writing files")
	includeRounds = flag.Bool("rounds", true, "generate round-level STT subtitles")
	includeClips  = flag.Bool("highlights", true, "generate highlight subtitles")
	limit         = flag.Int("limit", 0, "maximum subtitle files to generate, 0 means unlimited")
)

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "stt-subtitle", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()

	var c config
	app.MustLoadConfig(*configFile, &c)
	summary, err := subtitle.Backfill(c.RecordConf, subtitle.BackfillOptions{
		Force:      *force,
		ListOnly:   *listOnly,
		Rounds:     *includeRounds,
		Highlights: *includeClips,
		Limit:      *limit,
		Writer:     os.Stdout,
	})
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	fmt.Printf("subtitle backfill completed: round_generated=%d round_existing=%d round_no_cues=%d highlight_generated=%d highlight_existing=%d highlight_missing_stt=%d highlight_invalid_meta=%d highlight_no_cues=%d listed=%d\n",
		summary.RoundGenerated,
		summary.RoundSkippedExisting,
		summary.RoundSkippedNoCues,
		summary.HighlightGenerated,
		summary.HighlightSkippedExisting,
		summary.HighlightSkippedMissingSTT,
		summary.HighlightSkippedInvalidMeta,
		summary.HighlightSkippedNoCues,
		summary.Listed,
	)
}

package main

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pkg/errors"

	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/highlightclip"
	"scutbot.cn/web/rm-monitor/ent/mediaartifact"
	"scutbot.cn/web/rm-monitor/ent/recordtask"
	"scutbot.cn/web/rm-monitor/ent/uploadtask"
	"scutbot.cn/web/rm-monitor/pkg/app"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
	"scutbot.cn/web/rm-monitor/pkg/kubejob"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
	"scutbot.cn/web/rm-monitor/pkg/subtitle"
)

type config struct {
	PostgresConf common.PostgresConf
	RecordConf   common.RecordConf
	UploadConf   common.UploadConf
	K8sJobConf   common.K8sJobConf `json:",optional"`
}

type roundRow struct {
	MatchID    string
	RoundID    int
	RoundNo    int
	SourcePath string
	Event      string
	Zone       string
}

type summary struct {
	RoundsScanned      int
	SubtitlesGenerated int
	SubtitlesExisting  int
	SubtitlesNoCues    int
	AudioRemoved       int
	AudioKept          int
	HighlightsReset    int
	HighlightJobsGone  int
	RecoveredResults   int
}

var (
	configFile       = flag.String("f", "etc/config.yml", "config file")
	apply            = flag.Bool("apply", false, "apply repairs; default is dry-run")
	eventFilter      = flag.String("event", "", "match event filter")
	zoneFilter       = flag.String("zone", "", "match zone filter")
	matchIDsFlag     = flag.String("match", "", "comma separated match ids")
	roundIDsFlag     = flag.String("round", "", "comma separated match_round ids")
	limit            = flag.Int("limit", 0, "maximum rounds/highlights to process, 0 means unlimited")
	repairSubtitle   = flag.Bool("subtitle", true, "generate missing round subtitle from stt.jsonl")
	cleanupAudio     = flag.Bool("cleanup-audio", true, "delete audio dir after successful stt and ffmpeg done marker")
	resetHighlights  = flag.Bool("reset-highlights", true, "reset failed highlight clips caused by known recoverable artifact failures")
	recoverResults   = flag.Bool("recover-results", true, "recover tasks wrongly marked failed when result.json exists")
	deleteK8sJobs    = flag.Bool("delete-k8s-jobs", true, "delete existing failed highlight jobs before resetting clips; only works in cluster")
	highlightErrLike = flag.String("highlight-error-like", "ffmpeg cover failed", "substring used to find recoverable failed highlight clips")
)

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "recording-repair", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	var c config
	if err := app.LoadConfig(*configFile, &c); err != nil {
		fatal(err)
	}
	c.RecordConf = c.RecordConf.WithDefaults()
	ctx := context.Background()

	sqlDB, err := stdsql.Open("pgx", c.PostgresConf.DSN)
	if err != nil {
		fatal(err)
	}
	defer sqlDB.Close()

	entClient, err := db.Open(ctx, common.PostgresConf{DSN: c.PostgresConf.DSN})
	if err != nil {
		fatal(err)
	}
	defer entClient.Close()

	rows, err := queryRounds(ctx, sqlDB, c.RecordConf.STTRole, *eventFilter, *zoneFilter, parseStringList(*matchIDsFlag), parseIntList(*roundIDsFlag), *limit)
	if err != nil {
		fatal(err)
	}
	var s summary
	if *repairSubtitle || *cleanupAudio {
		if err := repairRounds(c.RecordConf, rows, *apply, &s); err != nil {
			fatal(err)
		}
	}
	if *resetHighlights {
		if err := resetRecoverableHighlights(ctx, c, entClient, *apply, &s); err != nil {
			fatal(err)
		}
	}
	if *recoverResults {
		if err := recoverCompletedResults(ctx, c, entClient, *apply, &s); err != nil {
			fatal(err)
		}
	}
	fmt.Printf("summary dry_run=%t rounds=%d subtitles_generated=%d subtitles_existing=%d subtitles_no_cues=%d audio_removed=%d audio_kept=%d highlights_reset=%d highlight_jobs_deleted=%d recovered_results=%d\n",
		!*apply,
		s.RoundsScanned,
		s.SubtitlesGenerated,
		s.SubtitlesExisting,
		s.SubtitlesNoCues,
		s.AudioRemoved,
		s.AudioKept,
		s.HighlightsReset,
		s.HighlightJobsGone,
		s.RecoveredResults,
	)
}

func queryRounds(ctx context.Context, db *stdsql.DB, role, event, zone string, matchIDs []string, roundIDs []int, limit int) ([]roundRow, error) {
	args := []any{strings.TrimSpace(role)}
	filters := []string{"rt.role = $1", "rt.status = 'SUCCEEDED'", "ma.kind = 'source'", "ma.status = 'AVAILABLE'", "mr.status = 'ENDED'"}
	if strings.TrimSpace(event) != "" {
		args = append(args, strings.TrimSpace(event))
		filters = append(filters, fmt.Sprintf("m.event = $%d", len(args)))
	}
	if strings.TrimSpace(zone) != "" {
		args = append(args, strings.TrimSpace(zone))
		filters = append(filters, fmt.Sprintf("m.zone = $%d", len(args)))
	}
	if len(matchIDs) > 0 {
		holders := make([]string, 0, len(matchIDs))
		for _, id := range matchIDs {
			args = append(args, id)
			holders = append(holders, fmt.Sprintf("$%d", len(args)))
		}
		filters = append(filters, "m.id in ("+strings.Join(holders, ",")+")")
	}
	if len(roundIDs) > 0 {
		holders := make([]string, 0, len(roundIDs))
		for _, id := range roundIDs {
			args = append(args, id)
			holders = append(holders, fmt.Sprintf("$%d", len(args)))
		}
		filters = append(filters, "mr.id in ("+strings.Join(holders, ",")+")")
	}
	limitSQL := ""
	if limit > 0 {
		args = append(args, limit)
		limitSQL = fmt.Sprintf(" limit $%d", len(args))
	}
	query := `
		select m.id, mr.id, mr.round_no, ma.path, m.event, m.zone
		from media_artifacts ma
		join record_tasks rt on rt.id = ma.record_task_media_artifacts
		join match_rounds mr on mr.id = rt.match_round_record_tasks
		join matches m on m.id = mr.match_rounds
		where ` + strings.Join(filters, " and ") + `
		order by m.event, m.zone, m.order, mr.round_no` + limitSQL
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, errors.Wrap(err, "query affected rounds")
	}
	defer rows.Close()
	var out []roundRow
	for rows.Next() {
		var r roundRow
		if err := rows.Scan(&r.MatchID, &r.RoundID, &r.RoundNo, &r.SourcePath, &r.Event, &r.Zone); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func repairRounds(conf common.RecordConf, rows []roundRow, apply bool, s *summary) error {
	for _, r := range rows {
		s.RoundsScanned++
		roundDir := storagepath.Resolve(conf.BaseDir, pathpkg.Dir(filepath.ToSlash(r.SourcePath)))
		sttPath := filepath.Join(roundDir, "stt.jsonl")
		subtitlePath := filepath.Join(roundDir, fmt.Sprintf("%s.srt", conf.STTRole))
		audioDir := filepath.Join(roundDir, "audio")
		if !hasSuccessfulSTT(sttPath) {
			fmt.Printf("skip round=%d match=%s: no successful stt at %s\n", r.RoundID, r.MatchID, sttPath)
			continue
		}
		if *repairSubtitle {
			if _, err := os.Stat(subtitlePath); err == nil {
				s.SubtitlesExisting++
			} else if os.IsNotExist(err) {
				fmt.Printf("%s subtitle round=%d path=%s\n", action(apply), r.RoundID, subtitlePath)
				if apply {
					err := subtitle.WriteSRTFromJSONL(sttPath, subtitlePath, subtitle.Options{})
					if errors.Is(err, subtitle.ErrNoCues) {
						s.SubtitlesNoCues++
					} else if err != nil {
						return errors.Wrapf(err, "write subtitle for round %d", r.RoundID)
					} else {
						s.SubtitlesGenerated++
					}
				} else {
					s.SubtitlesGenerated++
				}
			} else {
				return errors.Wrapf(err, "stat subtitle for round %d", r.RoundID)
			}
		}
		if *cleanupAudio {
			if stat, err := os.Stat(audioDir); err == nil && stat.IsDir() {
				if !fileExists(filepath.Join(audioDir, ".ffmpeg.done")) && !fileExists(filepath.Join(audioDir, ".ffmpeg.no_audio")) {
					s.AudioKept++
					fmt.Printf("keep audio round=%d: no done marker %s\n", r.RoundID, audioDir)
					continue
				}
				fmt.Printf("%s audio round=%d dir=%s\n", action(apply), r.RoundID, audioDir)
				if apply {
					if err := os.RemoveAll(audioDir); err != nil {
						return errors.Wrapf(err, "remove audio dir for round %d", r.RoundID)
					}
				}
				s.AudioRemoved++
			}
		}
	}
	return nil
}

func resetRecoverableHighlights(ctx context.Context, c config, client *ent.Client, apply bool, s *summary) error {
	q := client.HighlightClip.Query().
		Where(highlightclip.StatusEQ(highlightclip.StatusFAILED), highlightclip.ErrorMessageContains(*highlightErrLike))
	if *limit > 0 {
		q.Limit(*limit)
	}
	clips, err := q.All(ctx)
	if err != nil {
		return errors.Wrap(err, "query recoverable highlights")
	}
	var k8s *kubejob.Client
	if apply && *deleteK8sJobs {
		k8s, err = kubejob.NewInClusterClient()
		if err != nil {
			fmt.Printf("warn: k8s client unavailable, failed jobs may block deterministic job names: %v\n", err)
		}
	}
	namespace := c.K8sJobConf.WithDefaults().Namespace
	if namespace == "" {
		namespace = "rm-monitor"
	}
	for _, clip := range clips {
		name := fmt.Sprintf("highlight-%d", clip.ID)
		if clip.K8sJobName != nil && *clip.K8sJobName != "" {
			name = *clip.K8sJobName
		}
		fmt.Printf("%s highlight clip=%d job=%s error=%s\n", action(apply), clip.ID, name, ptrString(clip.ErrorMessage))
		jobDir := storagepath.Resolve(c.RecordConf.BaseDir, pathpkg.Join(clip.OutputDir, ".job", name))
		if apply && k8s != nil {
			if err := k8s.DeleteJob(ctx, namespace, name); err != nil {
				return errors.Wrapf(err, "delete highlight job %s", name)
			}
			s.HighlightJobsGone++
		}
		if apply {
			if err := os.RemoveAll(jobDir); err != nil {
				return errors.Wrapf(err, "remove highlight job dir %s", jobDir)
			}
			if err := client.HighlightClip.UpdateOneID(clip.ID).
				SetStatus(highlightclip.StatusPENDING).
				ClearK8sJobName().
				ClearErrorMessage().
				ClearStartedAt().
				ClearCompletedAt().
				SetUpdatedAt(time.Now()).
				Exec(ctx); err != nil {
				return errors.Wrapf(err, "reset highlight clip %d", clip.ID)
			}
		}
		s.HighlightsReset++
	}
	return nil
}

func recoverCompletedResults(ctx context.Context, c config, client *ent.Client, apply bool, s *summary) error {
	if err := recoverUploadResults(ctx, c, client, apply, s); err != nil {
		return err
	}
	if err := recoverHighlightResults(ctx, c, client, apply, s); err != nil {
		return err
	}
	if err := recoverRecordResults(ctx, c, client, apply, s); err != nil {
		return err
	}
	return nil
}

func recoverUploadResults(ctx context.Context, c config, client *ent.Client, apply bool, s *summary) error {
	tasks, err := client.UploadTask.Query().
		Where(uploadtask.StatusEQ(uploadtask.StatusFAILED), uploadtask.ErrorMessageContains("without result")).
		Limit(limitOrDefault()).
		All(ctx)
	if err != nil {
		return errors.Wrap(err, "query failed upload tasks with missing result")
	}
	baseDir := c.UploadConf.WithDefaults().BaseDir
	for _, task := range tasks {
		path := filepath.Join(filepath.Dir(storagepath.Resolve(baseDir, task.SourcePath)), jobcontract.DirName, fmt.Sprintf("upload-%d", task.ID), jobcontract.ResultFile)
		var result jobcontract.UploadResult
		if !readJSONIfExists(path, &result) {
			continue
		}
		completedAt := result.CompletedAt
		if completedAt.IsZero() {
			completedAt = time.Now()
		}
		fmt.Printf("%s upload result task=%d path=%s\n", action(apply), task.ID, path)
		if apply {
			update := client.UploadTask.UpdateOneID(task.ID).
				SetStatus(uploadtask.StatusSUCCEEDED).
				SetCompletedAt(completedAt).
				SetAttachmentFileToken(result.AttachmentFileToken).
				ClearErrorMessage()
			if result.BitableRecordURL != "" {
				update.SetBitableRecordURL(result.BitableRecordURL)
			}
			if err := update.Exec(ctx); err != nil {
				return errors.Wrapf(err, "recover upload task %d", task.ID)
			}
		}
		s.RecoveredResults++
	}
	return nil
}

func recoverHighlightResults(ctx context.Context, c config, client *ent.Client, apply bool, s *summary) error {
	clips, err := client.HighlightClip.Query().
		Where(highlightclip.StatusEQ(highlightclip.StatusFAILED), highlightclip.ErrorMessageContains("without result")).
		Limit(limitOrDefault()).
		All(ctx)
	if err != nil {
		return errors.Wrap(err, "query failed highlight clips with missing result")
	}
	baseDir := c.RecordConf.WithDefaults().BaseDir
	for _, clip := range clips {
		job := fmt.Sprintf("highlight-%d", clip.ID)
		path := storagepath.Resolve(baseDir, pathpkg.Join(clip.OutputDir, jobcontract.DirName, job, jobcontract.ResultFile))
		var result struct {
			OutputDir    string          `json:"output_dir"`
			Title        string          `json:"title"`
			Description  string          `json:"description"`
			Tags         []string        `json:"tags"`
			ModelPayload json.RawMessage `json:"model_payload"`
			CompletedAt  time.Time       `json:"completed_at"`
		}
		if !readJSONIfExists(path, &result) {
			continue
		}
		modelPayload := strings.TrimSpace(string(result.ModelPayload))
		if modelPayload == "" {
			modelPayload = "{}"
		}
		completedAt := result.CompletedAt
		if completedAt.IsZero() {
			completedAt = time.Now()
		}
		fmt.Printf("%s highlight result clip=%d path=%s\n", action(apply), clip.ID, path)
		if apply {
			if err := client.HighlightClip.UpdateOneID(clip.ID).
				SetStatus(highlightclip.StatusSUCCEEDED).
				SetOutputDir(result.OutputDir).
				SetTitle(result.Title).
				SetDescription(result.Description).
				SetTags(result.Tags).
				SetModelPayload(modelPayload).
				SetCompletedAt(completedAt).
				ClearErrorMessage().
				Exec(ctx); err != nil {
				return errors.Wrapf(err, "recover highlight clip %d", clip.ID)
			}
		}
		s.RecoveredResults++
	}
	return nil
}

func recoverRecordResults(ctx context.Context, c config, client *ent.Client, apply bool, s *summary) error {
	tasks, err := client.RecordTask.Query().
		Where(recordtask.StatusEQ(recordtask.StatusFAILED), recordtask.ErrorMessageContains("without result")).
		Limit(limitOrDefault()).
		All(ctx)
	if err != nil {
		return errors.Wrap(err, "query failed record tasks with missing result")
	}
	baseDir := c.RecordConf.WithDefaults().BaseDir
	for _, task := range tasks {
		path := filepath.Join(baseDir, filepath.FromSlash(pathpkg.Dir(filepath.ToSlash(task.OutputPath))), jobcontract.DirName, fmt.Sprintf("record-%d", task.ID), jobcontract.ResultFile)
		var result jobcontract.RecordResult
		if !readJSONIfExists(path, &result) {
			continue
		}
		completedAt := result.CompletedAt
		if completedAt.IsZero() {
			completedAt = time.Now()
		}
		fmt.Printf("%s record result task=%d path=%s\n", action(apply), task.ID, path)
		if apply {
			if err := client.RecordTask.UpdateOneID(task.ID).
				SetStatus(recordtask.StatusSUCCEEDED).
				SetCompletedAt(completedAt).
				SetFileSize(result.FileSize).
				SetChecksum(result.Checksum).
				ClearErrorMessage().
				Exec(ctx); err != nil {
				return errors.Wrapf(err, "recover record task %d", task.ID)
			}
			if err := client.MediaArtifact.Create().
				SetRecordTaskID(result.RecordTaskID).
				SetKind(mediaartifact.KindSource).
				SetPath(result.OutputPath).
				SetFormat(mediaartifact.FormatFlv).
				SetCodec(mediaartifact.CodecCopy).
				SetFileSize(result.FileSize).
				SetChecksum(result.Checksum).
				SetStatus(mediaartifact.StatusAVAILABLE).
				OnConflictColumns(mediaartifact.RecordTaskColumn, mediaartifact.FieldKind).
				UpdateNewValues().
				Exec(ctx); err != nil {
				return errors.Wrapf(err, "recover source artifact for record task %d", task.ID)
			}
		}
		s.RecoveredResults++
	}
	return nil
}

func readJSONIfExists(path string, v any) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, v) == nil
}

func limitOrDefault() int {
	if *limit > 0 {
		return *limit
	}
	return 500
}

func hasSuccessfulSTT(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), `"status":"SUCCEEDED"`) || strings.Contains(string(data), `"status": "SUCCEEDED"`)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func parseStringList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseIntList(raw string) []int {
	parts := parseStringList(raw)
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil {
			fatal(errors.Wrapf(err, "parse int %q", p))
		}
		out = append(out, v)
	}
	return out
}

func action(apply bool) string {
	if apply {
		return "repair"
	}
	return "would repair"
}

func ptrString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func fatal(err error) {
	logx.Error(err)
	os.Exit(1)
}

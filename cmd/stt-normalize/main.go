package main

import (
	"bufio"
	"context"
	stdsql "database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pkg/errors"

	"scutbot.cn/web/rm-monitor/pkg/app"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
	"scutbot.cn/web/rm-monitor/pkg/stttext"
)

type config struct {
	PostgresConf common.PostgresConf
	RecordConf   common.RecordConf
}

type row struct {
	MatchID    string
	RoundID    int
	RoundNo    int
	SourcePath string
}

type normalizeResult struct {
	Path    string
	Changed bool
	Sample  string
}

func main() {
	var (
		configFile    = flag.String("f", "etc/config.yml", "config file, normally stt-job config")
		matchIDsFlag  = flag.String("match", "", "comma separated match ids")
		roundIDsFlag  = flag.String("round", "", "comma separated match_round ids")
		limit         = flag.Int("limit", 100, "candidate limit")
		dryRun        = flag.Bool("dry-run", false, "scan and print changes without writing files")
		noReportReset = flag.Bool("no-report-reset", false, "do not clear matches.report after normalizing stt")
		_             = flag.Bool("force-report-reset", false, "deprecated compatibility flag; report reset is already enabled by default")
	)
	flag.Parse()

	var c config
	if err := app.LoadConfig(*configFile, &c); err != nil {
		fatal(err)
	}
	c.RecordConf = c.RecordConf.WithDefaults()
	db, err := stdsql.Open("pgx", c.PostgresConf.DSN)
	if err != nil {
		fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	rows, err := queryRows(ctx, db, c.RecordConf.STTRole, parseIntList(*roundIDsFlag), parseStringList(*matchIDsFlag), *limit)
	if err != nil {
		fatal(err)
	}
	if len(rows) == 0 {
		fmt.Println("no candidate rounds found")
		return
	}
	converter, err := stttext.NewSimplifier()
	if err != nil {
		fatal(err)
	}
	changedMatches := map[string]struct{}{}
	changed := 0
	for _, r := range rows {
		path := filepath.Join(filepath.Dir(resolveRecordPath(c.RecordConf.BaseDir, r.SourcePath)), "stt.jsonl")
		result, err := normalizeFile(path, converter, *dryRun)
		if err != nil {
			fatal(errors.Wrapf(err, "normalize match=%s round=%d path=%s", r.MatchID, r.RoundID, path))
		}
		if !result.Changed {
			continue
		}
		changed++
		changedMatches[r.MatchID] = struct{}{}
		fmt.Printf("normalized match=%s round=%d roundNo=%d path=%s sample=%s\n", r.MatchID, r.RoundID, r.RoundNo, result.Path, result.Sample)
	}
	if changed == 0 {
		fmt.Println("no stt text needed normalization")
		return
	}
	if !*dryRun && !*noReportReset {
		if err := resetReports(ctx, db, changedMatches); err != nil {
			fatal(err)
		}
		fmt.Printf("cleared reports for %d matches\n", len(changedMatches))
	}
	fmt.Printf("summary normalized_files=%d dry_run=%t\n", changed, *dryRun)
}

func queryRows(ctx context.Context, db *stdsql.DB, role string, roundIDs []int, matchIDs []string, limit int) ([]row, error) {
	args := []any{}
	filters := []string{}
	if len(roundIDs) > 0 {
		holders := make([]string, 0, len(roundIDs))
		for _, id := range roundIDs {
			args = append(args, id)
			holders = append(holders, fmt.Sprintf("$%d", len(args)))
		}
		filters = append(filters, "mr.id in ("+strings.Join(holders, ",")+")")
	}
	if len(matchIDs) > 0 {
		holders := make([]string, 0, len(matchIDs))
		for _, id := range matchIDs {
			args = append(args, id)
			holders = append(holders, fmt.Sprintf("$%d", len(args)))
		}
		filters = append(filters, "m.id in ("+strings.Join(holders, ",")+")")
	}
	where := ""
	if len(filters) > 0 {
		where = " and (" + strings.Join(filters, " or ") + ")"
	}
	args = append(args, strings.TrimSpace(role), limit)
	roleArg := len(args) - 1
	limitArg := len(args)
	q := fmt.Sprintf(`
select m.id, mr.id, mr.round_no, ma.path
from matches m
join match_rounds mr on mr.match_rounds=m.id
join record_tasks rec on rec.match_round_record_tasks=mr.id
join media_artifacts ma on ma.record_task_media_artifacts=rec.id
where mr.status='ENDED'
  and rec.role=$%d
  and ma.kind='source'
  and ma.status='AVAILABLE'
  %s
order by m.created_at desc, mr.round_no asc
limit $%d`, roleArg, where, limitArg)
	result, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer result.Close()
	out := []row{}
	for result.Next() {
		var r row
		if err := result.Scan(&r.MatchID, &r.RoundID, &r.RoundNo, &r.SourcePath); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, result.Err()
}

func normalizeFile(path string, converter *stttext.Converter, dryRun bool) (normalizeResult, error) {
	in, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return normalizeResult{Path: path}, nil
		}
		return normalizeResult{}, err
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	lines := []string{}
	changed := false
	sample := ""
	for scanner.Scan() {
		line := scanner.Text()
		normalized, lineChanged, err := normalizeJSONLine(line, converter)
		if err != nil {
			return normalizeResult{}, err
		}
		if lineChanged && sample == "" {
			sample = sampleText(line, normalized)
		}
		changed = changed || lineChanged
		lines = append(lines, normalized)
	}
	if err := scanner.Err(); err != nil {
		_ = in.Close()
		return normalizeResult{}, err
	}
	if err := in.Close(); err != nil {
		return normalizeResult{}, err
	}
	if !changed || dryRun {
		return normalizeResult{Path: path, Changed: changed, Sample: sample}, nil
	}
	tmp := path + ".tmp"
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return normalizeResult{}, err
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(path)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return normalizeResult{}, err
	}
	return normalizeResult{Path: path, Changed: true, Sample: sample}, nil
}

func normalizeJSONLine(line string, converter *stttext.Converter) (string, bool, error) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		return "", false, err
	}
	changed := false
	if text, ok := obj["text"].(string); ok {
		next, err := converter.Simplify(text)
		if err != nil {
			return "", false, err
		}
		if next != text {
			obj["text"] = next
			changed = true
		}
	}
	if segments, ok := obj["segments"].([]any); ok {
		for _, raw := range segments {
			segment, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			text, ok := segment["text"].(string)
			if !ok {
				continue
			}
			next, err := converter.Simplify(text)
			if err != nil {
				return "", false, err
			}
			if next != text {
				segment["text"] = next
				changed = true
			}
		}
	}
	if !changed {
		return line, false, nil
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return "", false, err
	}
	return string(out), true, nil
}

func sampleText(before, after string) string {
	if len(before) > 80 {
		before = before[:80]
	}
	if len(after) > 80 {
		after = after[:80]
	}
	return before + " -> " + after
}

func resetReports(ctx context.Context, db *stdsql.DB, matchSet map[string]struct{}) error {
	for matchID := range matchSet {
		if _, err := db.ExecContext(ctx, "update matches set report=null, updated_at=now() where id=$1", matchID); err != nil {
			return err
		}
	}
	return nil
}

func resolveRecordPath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return storagepath.Resolve(baseDir, path)
}

func parseIntList(raw string) []int {
	parts := parseStringList(raw)
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		id, err := strconv.Atoi(p)
		if err == nil {
			out = append(out, id)
		}
	}
	return out
}

func parseStringList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

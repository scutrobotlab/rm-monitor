package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/highlightclip"
	"scutbot.cn/web/rm-monitor/highlight-artifact-job/internal/config"
	"scutbot.cn/web/rm-monitor/pkg/app"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
)

var (
	configFile = flag.String("f", "etc/config.yml", "the config file")
	clipIDFlag = flag.Int("clip", 0, "highlight clip id")
)

const (
	highlightVideoFile = "video.mp4"
	highlightDanmuFile = "video.danmuku.xml"
)

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "highlight-artifact-job", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	if *clipIDFlag == 0 {
		logx.Error("clip id is required")
		os.Exit(1)
	}
	var c config.Config
	app.MustLoadConfig(*configFile, &c)
	client, err := db.Open(context.Background(), c.PostgresConf)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	defer client.Close()
	if err := run(context.Background(), client, c, *clipIDFlag); err != nil {
		logx.Error(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, client *ent.Client, c config.Config, clipID int) error {
	clip, err := client.HighlightClip.Query().
		Where(highlightclip.ID(clipID)).
		WithMatchRound(func(q *ent.MatchRoundQuery) {
			q.WithMatch(func(q *ent.MatchQuery) { q.WithRedTeam().WithBlueTeam() })
		}).
		WithSourceArtifact().
		Only(ctx)
	if err != nil {
		return errors.Wrap(err, "get highlight clip")
	}
	source := clip.Edges.SourceArtifact
	round := clip.Edges.MatchRound
	if source == nil || round == nil || round.Edges.Match == nil {
		return fail(ctx, client, clipID, "highlight clip missing source artifact or match round")
	}
	recordConf := c.RecordConf.WithDefaults()
	sourceRel, sourcePath, err := artifactPath(recordConf.BaseDir, source.Path)
	if err != nil {
		return fail(ctx, client, clipID, err.Error())
	}
	roundRelDir := pathpkg.Dir(sourceRel)
	roundDir := storagepath.Resolve(recordConf.BaseDir, roundRelDir)
	sttLines, err := readSTT(filepath.Join(roundDir, "stt.jsonl"), clip.StartSeconds, clip.EndSeconds)
	if err != nil {
		return fail(ctx, client, clipID, err.Error())
	}
	danmuFile := filepath.Join(roundDir, fmt.Sprintf("%s.danmuku.xml", clip.Role))
	danmuLines, err := readDanmu(danmuFile, clip.StartSeconds, clip.EndSeconds)
	if err != nil {
		return fail(ctx, client, clipID, err.Error())
	}
	llm, err := generateLLM(ctx, c.LLMConf.WithDefaults(), clip, sttLines, danmuLines)
	if err != nil {
		return fail(ctx, client, clipID, err.Error())
	}
	outputRel := pathpkg.Join(roundRelDir, "highlights", highlightDirName(clip.HighlightIndex, llm.Title))
	outputDir := storagepath.Resolve(recordConf.BaseDir, outputRel)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fail(ctx, client, clipID, errors.Wrap(err, "create output dir").Error())
	}
	if err := sliceVideo(ctx, sourcePath, filepath.Join(outputDir, highlightVideoFile), clip.StartSeconds, clip.EndSeconds); err != nil {
		return fail(ctx, client, clipID, err.Error())
	}
	if err := writeCroppedDanmu(danmuFile, filepath.Join(outputDir, highlightDanmuFile), clip.StartSeconds, clip.EndSeconds); err != nil {
		return fail(ctx, client, clipID, err.Error())
	}
	payload := buildHighlightJSON(clip, sourceRel, outputRel, llm)
	if err := writeJSON(filepath.Join(outputDir, "highlight.json"), payload); err != nil {
		return fail(ctx, client, clipID, err.Error())
	}
	modelPayload, _ := json.Marshal(llm)
	now := time.Now()
	err = client.HighlightClip.UpdateOneID(clipID).
		SetStatus(highlightclip.StatusSUCCEEDED).
		SetOutputDir(outputRel).
		SetTitle(llm.Title).
		SetDescription(llm.Description).
		SetTags(llm.Tags).
		SetModelPayload(string(modelPayload)).
		SetCompletedAt(now).
		ClearErrorMessage().
		Exec(ctx)
	if err != nil {
		return errors.Wrap(err, "mark highlight succeeded")
	}
	return nil
}

func fail(ctx context.Context, client *ent.Client, clipID int, msg string) error {
	_ = client.HighlightClip.UpdateOneID(clipID).SetStatus(highlightclip.StatusFAILED).SetErrorMessage(msg).Exec(ctx)
	return errors.New(msg)
}

func sliceVideo(ctx context.Context, sourcePath, outputPath string, start, end float64) error {
	tmp := outputPath + ".part"
	_ = os.Remove(tmp)
	duration := math.Max(0, end-start)
	cmd := exec.CommandContext(ctx,
		"ffmpeg",
		"-hide_banner",
		"-loglevel", "info",
		"-nostdin",
		"-i", sourcePath,
		"-ss", fmt.Sprintf("%.3f", start),
		"-t", fmt.Sprintf("%.3f", duration),
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-sn",
		"-dn",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "23",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-b:a", "128k",
		"-movflags", "+faststart",
		"-f", "mp4",
		"-y", tmp,
	)
	var stderr bytes.Buffer
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmp)
		return errors.New(commandError(err, stderr.String()))
	}
	stat, err := os.Stat(tmp)
	if err != nil {
		return errors.Wrap(err, "stat highlight video")
	}
	if stat.Size() == 0 {
		_ = os.Remove(tmp)
		return errors.New("highlight video is empty")
	}
	if err := validateMP4Output(ctx, tmp, duration); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, outputPath)
}

func validateMP4Output(ctx context.Context, outputPath string, expectedDuration float64) error {
	cmd := exec.CommandContext(ctx,
		"ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		outputPath,
	)
	out, err := cmd.Output()
	if err != nil {
		return errors.Wrap(err, "probe highlight video")
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || duration <= 0 {
		return errors.Errorf("highlight video has invalid duration %q", strings.TrimSpace(string(out)))
	}
	if expectedDuration > 0 && duration > expectedDuration+10 {
		return errors.Errorf("highlight video duration %.3fs exceeds expected %.3fs", duration, expectedDuration)
	}
	return nil
}

func artifactPath(baseDir, artifactPath string) (string, string, error) {
	p := pathpkg.Clean(filepath.ToSlash(strings.TrimSpace(artifactPath)))
	if p == "." || p == "/" {
		return "", "", errors.New("artifact path is empty")
	}
	if strings.HasPrefix(p, "../") || p == ".." {
		return "", "", errors.Errorf("artifact path %q escapes records root", artifactPath)
	}
	if strings.HasPrefix(p, "/") {
		base := pathpkg.Clean(filepath.ToSlash(baseDir))
		prefix := strings.TrimSuffix(base, "/") + "/"
		if !strings.HasPrefix(p, prefix) {
			return "", "", errors.Errorf("artifact path %q is outside base dir %q", artifactPath, baseDir)
		}
		p = strings.TrimPrefix(p, prefix)
	}
	return p, filepath.Join(baseDir, filepath.FromSlash(p)), nil
}

type sttLine struct {
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
	Status string  `json:"status"`
	Text   string  `json:"text"`
}

func readSTT(path string, start, end float64) ([]sttLine, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "read stt jsonl")
	}
	var all []sttLine
	var out []sttLine
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row sttLine
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		if row.Status != "SUCCEEDED" || strings.TrimSpace(row.Text) == "" {
			continue
		}
		all = append(all, row)
		if row.End >= start && row.Start <= end {
			out = append(out, row)
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	if len(all) == 0 {
		return nil, errors.New("no stt text available")
	}
	const contextSeconds = 60.0
	for _, row := range all {
		if row.End >= start-contextSeconds && row.Start <= end+contextSeconds {
			out = append(out, row)
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	return nearestSTT(all, start, end), nil
}

func nearestSTT(lines []sttLine, start, end float64) []sttLine {
	center := (start + end) / 2
	best := make([]sttLine, 0, 6)
	for _, line := range lines {
		d := math.Abs(((line.Start + line.End) / 2) - center)
		inserted := false
		for i, existing := range best {
			existingD := math.Abs(((existing.Start + existing.End) / 2) - center)
			if d < existingD {
				best = append(best, sttLine{})
				copy(best[i+1:], best[i:])
				best[i] = line
				inserted = true
				break
			}
		}
		if !inserted {
			best = append(best, line)
		}
		if len(best) > 6 {
			best = best[:6]
		}
	}
	return best
}

type danmuText struct {
	Time float64
	Text string
}

func readDanmu(path string, start, end float64) ([]danmuText, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "read danmu xml")
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var out []danmuText
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.Wrap(err, "parse danmu xml")
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "d" {
			continue
		}
		var node danmuNode
		if err := dec.DecodeElement(&node, &se); err != nil {
			return nil, err
		}
		t, ok := parseDanmuTime(node.P)
		if ok && t >= start && t <= end {
			out = append(out, danmuText{Time: t, Text: strings.TrimSpace(node.Text)})
		}
	}
	return out, nil
}

type danmuNode struct {
	XMLName xml.Name   `xml:"d"`
	P       string     `xml:"p,attr"`
	Attrs   []xml.Attr `xml:",any,attr"`
	Text    string     `xml:",chardata"`
}

func writeCroppedDanmu(input, output string, start, end float64) error {
	raw, err := os.ReadFile(input)
	if err != nil {
		return errors.Wrap(err, "read danmu xml")
	}
	tmp := output + ".part"
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := xml.NewEncoder(f)
	_, _ = f.WriteString(xml.Header)
	if err := enc.EncodeToken(xml.StartElement{Name: xml.Name{Local: "i"}}); err != nil {
		_ = f.Close()
		return err
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = f.Close()
			return errors.Wrap(err, "parse danmu xml")
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "d" {
			continue
		}
		var node danmuNode
		if err := dec.DecodeElement(&node, &se); err != nil {
			_ = f.Close()
			return err
		}
		t, ok := parseDanmuTime(node.P)
		if !ok || t < start || t > end {
			continue
		}
		node.P = rewriteDanmuTime(node.P, t-start)
		if err := enc.Encode(node); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := enc.EncodeToken(xml.EndElement{Name: xml.Name{Local: "i"}}); err != nil {
		_ = f.Close()
		return err
	}
	if err := enc.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, output)
}

func parseDanmuTime(p string) (float64, bool) {
	parts := strings.Split(p, ",")
	if len(parts) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(parts[0], 64)
	return v, err == nil
}

func rewriteDanmuTime(p string, t float64) string {
	parts := strings.Split(p, ",")
	if len(parts) == 0 {
		return fmt.Sprintf("%.3f", t)
	}
	parts[0] = fmt.Sprintf("%.3f", math.Max(0, t))
	return strings.Join(parts, ",")
}

type llmOutput struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Explanation string   `json:"explanation"`
}

func generateLLM(ctx context.Context, conf common.LLMConf, clip *ent.HighlightClip, stt []sttLine, danmu []danmuText) (llmOutput, error) {
	conf = conf.WithDefaults()
	if strings.TrimSpace(conf.BaseURL) == "" || strings.TrimSpace(conf.APIKey) == "" || strings.TrimSpace(conf.Model) == "" {
		return llmOutput{}, errors.New("highlight llm config is incomplete")
	}
	input, err := json.Marshal(map[string]interface{}{
		"start_seconds": clip.StartSeconds,
		"end_seconds":   clip.EndSeconds,
		"peak_seconds":  clip.PeakSeconds,
		"score":         clip.Score,
		"stt":           stt,
		"danmu":         danmu,
	})
	if err != nil {
		return llmOutput{}, err
	}
	client := openai.NewClient(
		option.WithBaseURL(strings.TrimRight(strings.TrimSpace(conf.BaseURL), "/")),
		option.WithAPIKey(conf.APIKey),
		option.WithHTTPClient(&http.Client{Timeout: time.Duration(conf.TimeoutSeconds) * time.Second}),
	)
	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: openai.ChatModel(conf.Model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("你是 RoboMaster 赛事短视频编辑。只输出 JSON，对象字段为 title、description、tags、explanation。title 不超过 20 个中文字符，description 不超过 80 个中文字符，tags 是中文短标签数组。不要编造输入没有的击杀、战术或比分。"),
			openai.UserMessage(string(input)),
		},
		Temperature: openai.Float(0.2),
	})
	if err != nil {
		return llmOutput{}, err
	}
	if len(completion.Choices) == 0 {
		return llmOutput{}, errors.New("llm returned no choices")
	}
	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	var out llmOutput
	if err := json.Unmarshal([]byte(stripCodeFence(content)), &out); err != nil {
		return llmOutput{}, errors.Wrap(err, "parse highlight llm json")
	}
	if strings.TrimSpace(out.Title) == "" || strings.TrimSpace(out.Description) == "" {
		return llmOutput{}, errors.New("highlight llm output missing title or description")
	}
	return out, nil
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) >= 3 {
		return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
	}
	return s
}

func highlightDirName(index int, title string) string {
	base := fmt.Sprintf("Highlight-%02d", index)
	title = sanitizeSegment(title)
	if title == "" {
		return base
	}
	return base + "-" + title
}

var invalidSegment = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]+`)

func sanitizeSegment(s string) string {
	s = strings.TrimSpace(invalidSegment.ReplaceAllString(s, "_"))
	s = strings.Trim(s, ". ")
	if len([]rune(s)) > 40 {
		r := []rune(s)
		s = string(r[:40])
	}
	return s
}

func buildHighlightJSON(clip *ent.HighlightClip, sourceRel, outputRel string, llm llmOutput) map[string]interface{} {
	return map[string]interface{}{
		"schema":            "rm-monitor/highlight/v1",
		"highlight_clip_id": clip.ID,
		"highlight_index":   clip.HighlightIndex,
		"role":              clip.Role,
		"algorithm_version": clip.AlgorithmVersion,
		"start_seconds":     clip.StartSeconds,
		"end_seconds":       clip.EndSeconds,
		"peak_seconds":      clip.PeakSeconds,
		"score":             clip.Score,
		"title":             llm.Title,
		"description":       llm.Description,
		"tags":              llm.Tags,
		"explanation":       llm.Explanation,
		"source_artifact":   sourceRel,
		"output_dir":        outputRel,
		"video":             pathpkg.Join(outputRel, highlightVideoFile),
		"danmu":             pathpkg.Join(outputRel, highlightDanmuFile),
		"publish":           map[string]string{"status": "disabled"},
	}
}

func writeJSON(path string, v interface{}) error {
	tmp := path + ".part"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func commandError(err error, stderr string) string {
	const max = 2048
	msg := err.Error()
	if stderr != "" {
		if len(stderr) > max {
			stderr = stderr[len(stderr)-max:]
		}
		msg = fmt.Sprintf("%s: %s", msg, stderr)
	}
	return msg
}

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/highlightclip"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/highlight-job/internal/config"
	"scutbot.cn/web/rm-monitor/pkg/app"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/difyworkflow"
	"scutbot.cn/web/rm-monitor/pkg/highlight"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
)

var configFile = flag.String("f", "etc/config.yml", "the config file")

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "highlight-job", Mode: "console", Encoding: "plain"})
}

type Context struct {
	Schema       string  `json:"schema"`
	MatchID      string  `json:"match_id"`
	MatchRoundID int     `json:"match_round_id"`
	RoundNo      int     `json:"round_no"`
	SourcePath   string  `json:"source_path"`
	RoundDir     string  `json:"round_dir"`
	Role         string  `json:"role"`
	Event        string  `json:"event"`
	Zone         string  `json:"zone"`
	Order        int     `json:"order"`
	MatchType    string  `json:"match_type"`
	RedSchool    string  `json:"red_school"`
	RedName      string  `json:"red_name"`
	BlueSchool   string  `json:"blue_school"`
	BlueName     string  `json:"blue_name"`
	Priority     int     `json:"priority"`
	TrimStart    float64 `json:"trim_start_seconds,omitempty"`
	TrimEnd      float64 `json:"trim_end_seconds,omitempty"`
}

type ArtifactContext struct {
	HighlightClipID    int             `json:"highlight_clip_id"`
	HighlightIndex     int             `json:"highlight_index"`
	Role               string          `json:"role"`
	AlgorithmVersion   string          `json:"algorithm_version"`
	StartSeconds       float64         `json:"start_seconds"`
	EndSeconds         float64         `json:"end_seconds"`
	PeakSeconds        float64         `json:"peak_seconds"`
	Score              float64         `json:"score"`
	SourceArtifactPath string          `json:"source_artifact_path"`
	RoundDir           string          `json:"round_dir"`
	OutputDir          string          `json:"output_dir"`
	Event              string          `json:"event"`
	Zone               string          `json:"zone"`
	Order              int             `json:"order"`
	MatchType          string          `json:"match_type"`
	RoundNo            int             `json:"round_no"`
	RedSchool          string          `json:"red_school"`
	RedName            string          `json:"red_name"`
	BlueSchool         string          `json:"blue_school"`
	BlueName           string          `json:"blue_name"`
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	Tags               []string        `json:"tags"`
	HighlightType      string          `json:"highlight_type"`
	PublishCaption     string          `json:"publish_caption"`
	ModelPayload       json.RawMessage `json:"model_payload"`
	PreviewSeconds     int             `json:"preview_seconds"`
	PreviewFPS         int             `json:"preview_fps"`
	PreviewWidth       int             `json:"preview_width"`
}

type Review struct {
	Accepted       bool     `json:"accepted"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Tags           []string `json:"tags"`
	HighlightType  string   `json:"highlight_type"`
	PublishCaption string   `json:"publish_caption"`
	Reason         string   `json:"reason"`
}

func main() {
	flag.Parse()
	var c config.Config
	app.MustLoadConfig(*configFile, &c)
	var jobCtx Context
	if err := jobcontract.ContextFromEnv(&jobCtx); err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	if err := jobcontract.WriteContext("", jobCtx); err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	client, err := db.Open(context.Background(), c.PostgresConf)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	defer client.Close()
	if err := run(context.Background(), client, c, jobCtx); err != nil {
		_ = jobcontract.WriteError("", "highlight", 0, err)
		logx.Error(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, client *ent.Client, c config.Config, jobCtx Context) error {
	conf := c.HighlightConf.WithDefaults()
	if !conf.Enabled {
		return writeContexts(nil)
	}
	if strings.TrimSpace(jobCtx.RoundDir) == "" || strings.TrimSpace(jobCtx.SourcePath) == "" || jobCtx.MatchRoundID == 0 {
		return errors.New("round_dir, source_path, and match_round_id are required")
	}
	recordConf := c.RecordConf.WithDefaults()
	roundDir := storagepath.Resolve(recordConf.BaseDir, jobCtx.RoundDir)
	stats, err := highlight.LoadDanmuStats(filepath.Join(roundDir, "stats", "danmu-count.json"))
	if err != nil {
		return errors.Wrap(err, "load danmu stats")
	}
	if !hasSuccessfulSTT(filepath.Join(roundDir, "stt.jsonl")) {
		return errors.New("stt.jsonl missing successful segments")
	}
	var online highlight.OnlineStats
	if p := filepath.Join(roundDir, "stats", "online-count.json"); fileExists(p) {
		online, _ = highlight.LoadOnlineStats(p)
	}
	candidates := highlight.FindCandidates(stats, online, conf)
	out := make([]ArtifactContext, 0, len(candidates))
	for _, candidate := range candidates {
		ctxForArtifact, err := createOrUpdateClip(ctx, client, c, jobCtx, conf, candidate)
		if err != nil {
			return err
		}
		if ctxForArtifact != nil {
			out = append(out, *ctxForArtifact)
		}
	}
	return writeContexts(out)
}

func createOrUpdateClip(ctx context.Context, client *ent.Client, c config.Config, jobCtx Context, conf common.HighlightConf, candidate highlight.Candidate) (*ArtifactContext, error) {
	outputDir := path.Join(jobCtx.RoundDir, "highlights", fmt.Sprintf("Highlight-%02d", candidate.Index))
	review, modelPayload, err := reviewCandidate(ctx, c, jobCtx, candidate, outputDir)
	if err != nil {
		return nil, err
	}
	create := client.HighlightClip.Create().
		SetMatchRoundID(jobCtx.MatchRoundID).
		SetHighlightIndex(candidate.Index).
		SetRole(conf.Role).
		SetAlgorithmVersion(conf.AlgorithmVersion).
		SetPriority(jobCtx.Priority).
		SetStartSeconds(candidate.Start).
		SetEndSeconds(candidate.End).
		SetPeakSeconds(candidate.Peak).
		SetScore(candidate.Score).
		SetSourcePath(jobCtx.SourcePath).
		SetOutputDir(outputDir).
		SetModelPayload(string(modelPayload)).
		SetCompletedAt(time.Now())
	if !review.Accepted {
		create.SetStatus(highlightclip.StatusSKIPPED).SetDescription(review.Reason)
	} else {
		create.SetStatus(highlightclip.StatusAVAILABLE).
			SetTitle(review.Title).
			SetDescription(review.Description).
			SetTags(review.Tags)
	}
	clip, err := create.Save(ctx)
	if err != nil {
		if !ent.IsConstraintError(err) {
			return nil, errors.Wrap(err, "create highlight clip")
		}
		update := client.HighlightClip.Update().
			Where(
				highlightclip.HasMatchRoundWith(matchround.ID(jobCtx.MatchRoundID)),
				highlightclip.RoleEQ(conf.Role),
				highlightclip.AlgorithmVersionEQ(conf.AlgorithmVersion),
				highlightclip.HighlightIndexEQ(candidate.Index),
			).
			SetPriority(jobCtx.Priority).
			SetStartSeconds(candidate.Start).
			SetEndSeconds(candidate.End).
			SetPeakSeconds(candidate.Peak).
			SetScore(candidate.Score).
			SetSourcePath(jobCtx.SourcePath).
			SetOutputDir(outputDir).
			SetModelPayload(string(modelPayload)).
			SetCompletedAt(time.Now())
		if !review.Accepted {
			update.SetStatus(highlightclip.StatusSKIPPED).SetDescription(review.Reason)
		} else {
			update.SetStatus(highlightclip.StatusAVAILABLE).SetTitle(review.Title).SetDescription(review.Description).SetTags(review.Tags)
		}
		if _, err := update.Save(ctx); err != nil {
			return nil, errors.Wrap(err, "update highlight clip")
		}
		clip, err = client.HighlightClip.Query().
			Where(highlightclip.HasMatchRoundWith(matchround.ID(jobCtx.MatchRoundID)), highlightclip.RoleEQ(conf.Role), highlightclip.AlgorithmVersionEQ(conf.AlgorithmVersion), highlightclip.HighlightIndexEQ(candidate.Index)).
			Only(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "query updated highlight clip")
		}
	}
	if !review.Accepted {
		return nil, nil
	}
	return &ArtifactContext{
		HighlightClipID:    clip.ID,
		HighlightIndex:     candidate.Index,
		Role:               conf.Role,
		AlgorithmVersion:   conf.AlgorithmVersion,
		StartSeconds:       candidate.Start,
		EndSeconds:         candidate.End,
		PeakSeconds:        candidate.Peak,
		Score:              candidate.Score,
		SourceArtifactPath: jobCtx.SourcePath,
		RoundDir:           jobCtx.RoundDir,
		OutputDir:          outputDir,
		Event:              jobCtx.Event,
		Zone:               jobCtx.Zone,
		Order:              jobCtx.Order,
		MatchType:          jobCtx.MatchType,
		RoundNo:            jobCtx.RoundNo,
		RedSchool:          jobCtx.RedSchool,
		RedName:            jobCtx.RedName,
		BlueSchool:         jobCtx.BlueSchool,
		BlueName:           jobCtx.BlueName,
		Title:              review.Title,
		Description:        review.Description,
		Tags:               review.Tags,
		HighlightType:      review.HighlightType,
		PublishCaption:     review.PublishCaption,
		ModelPayload:       modelPayload,
		PreviewSeconds:     conf.PreviewSeconds,
		PreviewFPS:         conf.PreviewFPS,
		PreviewWidth:       conf.PreviewWidth,
	}, nil
}

func reviewCandidate(ctx context.Context, c config.Config, jobCtx Context, candidate highlight.Candidate, outputDir string) (Review, json.RawMessage, error) {
	conf := c.HighlightConf.WithDefaults()
	if strings.TrimSpace(conf.ReviewWorkflowAPIKey) == "" {
		return Review{Accepted: true, Title: fmt.Sprintf("Round %d Highlight %02d", jobCtx.RoundNo, candidate.Index), Description: "弹幕峰值自动识别高光", Tags: []string{"RoboMaster", "赛事高光"}}, json.RawMessage(`{}`), nil
	}
	client, err := difyworkflow.New(c.DifyConf)
	if err != nil {
		return Review{}, nil, err
	}
	payload := map[string]any{
		"schema": "rm-monitor/dify-highlight-review-input/v1",
		"match": map[string]any{"event": jobCtx.Event, "zone": jobCtx.Zone, "order": jobCtx.Order, "match_type": jobCtx.MatchType, "red_school": jobCtx.RedSchool, "red_name": jobCtx.RedName, "blue_school": jobCtx.BlueSchool, "blue_name": jobCtx.BlueName},
		"round": map[string]any{"round_no": jobCtx.RoundNo, "role": jobCtx.Role},
		"candidate": map[string]any{"highlight_index": candidate.Index, "start_seconds": candidate.Start, "end_seconds": candidate.End, "peak_seconds": candidate.Peak, "score": candidate.Score, "source_path": jobCtx.SourcePath, "output_dir": outputDir},
	}
	rawPayload, _ := json.Marshal(payload)
	result, err := client.RunWorkflow(ctx, conf.ReviewWorkflowAPIKey, fmt.Sprintf("rm-monitor:highlight:%d:%d", jobCtx.MatchRoundID, candidate.Index), map[string]any{"payload": string(rawPayload)})
	if err != nil {
		return Review{}, nil, errors.Wrap(err, "run highlight review workflow")
	}
	raw, err := difyworkflow.RawOutput(result.Outputs, "highlight_review_json")
	if err != nil {
		return Review{}, nil, err
	}
	var review Review
	if err := json.Unmarshal(raw, &review); err != nil {
		return Review{}, nil, errors.Wrap(err, "decode highlight review")
	}
	if review.Accepted && review.Title == "" {
		review.Title = fmt.Sprintf("Round %d Highlight %02d", jobCtx.RoundNo, candidate.Index)
	}
	if review.Accepted && len(review.Tags) == 0 {
		review.Tags = []string{"RoboMaster", "赛事高光"}
	}
	modelPayload, _ := json.Marshal(map[string]any{"workflow_run_id": result.WorkflowRunID, "task_id": result.TaskID, "review": review})
	return review, modelPayload, nil
}

func writeContexts(contexts []ArtifactContext) error {
	raw, err := json.Marshal(contexts)
	if err != nil {
		return err
	}
	result := map[string]any{"highlight_contexts": contexts, "count": len(contexts)}
	if err := jobcontract.WriteTempResult(result); err != nil {
		return err
	}
	return jobcontract.WriteArgoOutputs(map[string]any{"highlight_contexts": string(raw), "highlight_count": len(contexts)})
}

func hasSuccessfulSTT(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, `"status":"SUCCEEDED"`) || strings.Contains(line, `"status": "SUCCEEDED"`) {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

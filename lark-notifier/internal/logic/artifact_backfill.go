package logic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/highlightclip"
	"scutbot.cn/web/rm-monitor/ent/larkmessage"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/pathfmt"
)

type ArtifactBackfillResult struct {
	Settlements   int `json:"settlements"`
	Highlights    int `json:"highlights"`
	CardSequences int `json:"card_sequences"`
}

func BackfillArtifactReadiness(ctx context.Context, client *ent.Client, configured common.RecordConf) (ArtifactBackfillResult, error) {
	var result ArtifactBackfillResult
	recordConf := configured.WithDefaults()
	baseDir := strings.TrimSpace(recordConf.BaseDir)
	if baseDir == "" {
		baseDir = "/records"
	}
	sequenceFloor := time.Now().Unix()
	updatedSequences, err := client.LarkMessage.Update().
		Where(larkmessage.CardSequenceLT(1_000_000_000)).
		SetCardSequence(sequenceFloor).
		Save(ctx)
	if err != nil {
		return result, errors.Wrap(err, "backfill legacy CardKit sequences")
	}
	result.CardSequences = updatedSequences
	rounds, err := client.MatchRound.Query().WithMatch(func(q *ent.MatchQuery) { q.WithRedTeam().WithBlueTeam() }).WithLarkBitableRecords().All(ctx)
	if err != nil {
		return result, errors.Wrap(err, "query rounds for artifact backfill")
	}
	for _, round := range rounds {
		if round.SettlementReadyAt != nil {
			continue
		}
		var roundDir string
		if len(round.Edges.LarkBitableRecords) > 0 {
			source := strings.TrimSpace(round.Edges.LarkBitableRecords[0].SourcePath)
			if source != "" {
				roundDir = filepath.Join(baseDir, filepath.Dir(filepath.FromSlash(source)))
			}
		}
		if roundDir == "" {
			m := round.Edges.Match
			if m == nil || m.Edges.RedTeam == nil || m.Edges.BlueTeam == nil {
				continue
			}
			matchDir, renderErr := pathfmt.RenderMatchDir(recordConf.MatchNameTemplate, recordConf.MatchDirTemplate, pathfmt.Data{
				Event: m.Event, Zone: m.Zone, Order: m.Order,
				RedSchool: m.Edges.RedTeam.SchoolName, RedName: m.Edges.RedTeam.Name,
				BlueSchool: m.Edges.BlueTeam.SchoolName, BlueName: m.Edges.BlueTeam.Name,
			})
			if renderErr != nil {
				continue
			}
			roundDir = filepath.Join(baseDir, filepath.FromSlash(matchDir), "Round-"+strconv.Itoa(round.RoundNo))
		}
		if !roundSettlementConfirmed(filepath.Join(roundDir, "round.json")) {
			continue
		}
		imagePath := filepath.Join(roundDir, "settlement.jpg")
		raw, readErr := os.ReadFile(imagePath)
		if readErr != nil || len(raw) == 0 {
			continue
		}
		sum := sha256.Sum256(raw)
		relativePath, relErr := filepath.Rel(baseDir, imagePath)
		if relErr != nil || strings.HasPrefix(relativePath, "..") {
			continue
		}
		relative := filepath.ToSlash(relativePath)
		if err := client.MatchRound.UpdateOneID(round.ID).
			SetSettlementStatus("CONFIRMED").
			SetSettlementImagePath(relative).
			SetSettlementImageChecksum(hex.EncodeToString(sum[:])).
			SetSettlementReadyAt(time.Now()).
			Exec(ctx); err != nil {
			return result, errors.Wrap(err, "backfill settlement readiness")
		}
		result.Settlements++
	}
	clips, err := client.HighlightClip.Query().
		Where(highlightclip.StatusEQ(highlightclip.StatusAVAILABLE), highlightclip.ArtifactReadyAtIsNil()).
		All(ctx)
	if err != nil {
		return result, errors.Wrap(err, "query highlights for artifact backfill")
	}
	for _, clip := range clips {
		valid := true
		for _, name := range []string{"video.mp4", "preview.gif", "highlight.json"} {
			info, statErr := os.Stat(filepath.Join(baseDir, filepath.FromSlash(clip.OutputDir), name))
			if statErr != nil || info.Size() <= 0 {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		previewRel := filepath.ToSlash(filepath.Join(clip.OutputDir, "preview.gif"))
		raw, readErr := os.ReadFile(filepath.Join(baseDir, filepath.FromSlash(previewRel)))
		if readErr != nil || len(raw) == 0 {
			continue
		}
		sum := sha256.Sum256(raw)
		if err := client.HighlightClip.UpdateOneID(clip.ID).
			SetPreviewPath(previewRel).
			SetPreviewChecksum(hex.EncodeToString(sum[:])).
			SetArtifactReadyAt(time.Now()).
			Exec(ctx); err != nil {
			return result, errors.Wrap(err, "backfill highlight readiness")
		}
		result.Highlights++
	}
	return result, nil
}

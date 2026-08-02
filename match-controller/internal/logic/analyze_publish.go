package logic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	common "scutbot.cn/web/rm-monitor/pkg/config"
)

func PublishAnalyzeResult(ctx context.Context, client *ent.Client, conf common.RecordConf, roundID int, roundJSONPath, imagePath, status string) error {
	if roundID <= 0 {
		return errors.New("match round id is required")
	}
	status = strings.ToUpper(strings.TrimSpace(status))
	update := client.MatchRound.UpdateOneID(roundID).SetSettlementStatus(status)
	if status != "CONFIRMED" {
		return update.ClearSettlementImagePath().ClearSettlementImageChecksum().ClearSettlementReadyAt().Exec(ctx)
	}
	roundRaw, err := os.ReadFile(roundJSONPath)
	if err != nil {
		return errors.Wrap(err, "read analyzed round json")
	}
	var roundResult struct {
		Settlement struct {
			Status string `json:"status"`
		} `json:"settlement"`
	}
	if err := json.Unmarshal(roundRaw, &roundResult); err != nil {
		return errors.Wrap(err, "decode analyzed round json")
	}
	if strings.ToUpper(roundResult.Settlement.Status) != "CONFIRMED" {
		return errors.Errorf("round json settlement status is %q", roundResult.Settlement.Status)
	}
	raw, err := os.ReadFile(imagePath)
	if err != nil {
		return errors.Wrap(err, "read settlement image")
	}
	if len(raw) == 0 {
		return errors.New("settlement image is empty")
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(raw)); err != nil {
		return errors.Wrap(err, "decode settlement image")
	}
	relative, err := relativeRecordPath(conf.WithDefaults().BaseDir, imagePath)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	return update.
		SetSettlementImagePath(relative).
		SetSettlementImageChecksum(hex.EncodeToString(sum[:])).
		SetSettlementReadyAt(time.Now()).
		Exec(ctx)
}

func relativeRecordPath(baseDir, path string) (string, error) {
	base, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.Errorf("path %q is outside records base %q", path, baseDir)
	}
	return filepath.ToSlash(rel), nil
}

package logic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/larkbitablerecord"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
)

type Result struct {
	Scanned int
	Deleted int
	Skipped int
}

func CleanUploadedSources(ctx context.Context, client *ent.Client, baseDir string, now time.Time, retentionDays int, limit int) (Result, error) {
	if limit <= 0 {
		limit = 100
	}
	if retentionDays <= 0 {
		retentionDays = 7
	}
	cutoff := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)
	records, err := client.LarkBitableRecord.Query().
		Where(
			larkbitablerecord.AttachmentFileTokenNotNil(),
			larkbitablerecord.SourceDeletedAtIsNil(),
			larkbitablerecord.UpdatedAtLTE(cutoff),
		).
		Order(larkbitablerecord.ByUpdatedAt()).
		Limit(limit).
		All(ctx)
	if err != nil {
		return Result{}, errors.Wrap(err, "query uploaded lark records")
	}
	result := Result{Scanned: len(records)}
	for _, record := range records {
		sourcePath := storagepath.Resolve(baseDir, record.SourcePath)
		archivePath := archivePathForSource(baseDir, record.SourcePath)
		if archivePath == "" {
			result.Skipped++
			continue
		}
		if _, err := os.Stat(archivePath); err != nil {
			if !os.IsNotExist(err) {
				logx.Errorf("stat archive %s failed: %v", archivePath, err)
			}
			result.Skipped++
			continue
		}
		if err := os.Remove(sourcePath); err != nil && !os.IsNotExist(err) {
			logx.Errorf("remove source %s failed: %v", sourcePath, err)
			result.Skipped++
			continue
		}
		if err := client.LarkBitableRecord.UpdateOneID(record.ID).
			SetSourceDeletedAt(now).
			Exec(ctx); err != nil {
			return result, errors.Wrap(err, "mark source deleted")
		}
		result.Deleted++
	}
	return result, nil
}

func archivePathForSource(baseDir, sourcePath string) string {
	if strings.TrimSpace(sourcePath) == "" {
		return ""
	}
	resolved := storagepath.Resolve(baseDir, sourcePath)
	ext := filepath.Ext(resolved)
	if ext == "" {
		return ""
	}
	return strings.TrimSuffix(resolved, ext) + ".mp4"
}

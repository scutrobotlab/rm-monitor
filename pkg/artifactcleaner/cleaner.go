package artifactcleaner

import (
	"context"
	"os"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/mediaartifact"
	"scutbot.cn/web/rm-monitor/ent/transcodetask"
	"scutbot.cn/web/rm-monitor/ent/uploadtask"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
)

type Result struct {
	Scanned int
	Deleted int
	Skipped int
}

func CleanExpiredSources(ctx context.Context, client *ent.Client, baseDir string, now time.Time, limit int) (Result, error) {
	if limit <= 0 {
		limit = 100
	}
	artifacts, err := client.MediaArtifact.Query().
		Where(
			mediaartifact.KindEQ(mediaartifact.KindSource),
			mediaartifact.StatusEQ(mediaartifact.StatusAVAILABLE),
			mediaartifact.DeletableAtNotNil(),
			mediaartifact.DeletableAtLTE(now),
		).
		WithUploadTask().
		WithSourceTranscodeTask(func(q *ent.TranscodeTaskQuery) {
			q.WithArchiveArtifact()
		}).
		Limit(limit).
		All(ctx)
	if err != nil {
		return Result{}, errors.Wrap(err, "query deletable sources")
	}
	result := Result{Scanned: len(artifacts)}
	for _, artifact := range artifacts {
		if !canDeleteSource(artifact) {
			result.Skipped++
			continue
		}
		fullPath := storagepath.Resolve(baseDir, artifact.Path)
		if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
			logx.Errorf("remove source artifact %s failed: %v", artifact.Path, err)
			result.Skipped++
			continue
		}
		if err := client.MediaArtifact.UpdateOneID(artifact.ID).
			SetStatus(mediaartifact.StatusDELETED).
			SetDeletedAt(now).
			Exec(ctx); err != nil {
			return result, errors.Wrap(err, "mark source deleted")
		}
		result.Deleted++
	}
	return result, nil
}

func canDeleteSource(artifact *ent.MediaArtifact) bool {
	transcode := artifact.Edges.SourceTranscodeTask
	if transcode == nil || transcode.Status != transcodetask.StatusSUCCEEDED || transcode.Edges.ArchiveArtifact == nil {
		return false
	}
	upload := artifact.Edges.UploadTask
	if upload == nil {
		return true
	}
	return upload.Status != uploadtask.StatusDISPATCHING && upload.Status != uploadtask.StatusRUNNING
}

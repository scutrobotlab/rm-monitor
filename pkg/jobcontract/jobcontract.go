package jobcontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
)

const (
	EnvName     = "RM_MONITOR_JOB_CONTEXT"
	DirName     = ".job"
	ContextFile = "context.json"
	ResultFile  = "result.json"
	ErrorFile   = "error.json"
)

type ErrorResult struct {
	Schema       string    `json:"schema"`
	TaskType     string    `json:"task_type"`
	TaskID       int       `json:"task_id"`
	Status       string    `json:"status"`
	ErrorMessage string    `json:"error_message"`
	CompletedAt  time.Time `json:"completed_at"`
}

type TranscodeContext struct {
	Schema              string `json:"schema"`
	TaskID              int    `json:"task_id"`
	SourceArtifactID    int    `json:"source_artifact_id"`
	RecordTaskID        int    `json:"record_task_id"`
	SourcePath          string `json:"source_path"`
	ArchivePath         string `json:"archive_path"`
	BaseDir             string `json:"base_dir"`
	SourceRetentionDays int    `json:"source_retention_days"`
}

type TranscodeResult struct {
	Schema           string    `json:"schema"`
	TaskID           int       `json:"task_id"`
	SourceArtifactID int       `json:"source_artifact_id"`
	RecordTaskID     int       `json:"record_task_id"`
	ArchivePath      string    `json:"archive_path"`
	Format           string    `json:"format"`
	Codec            string    `json:"codec"`
	FileSize         int64     `json:"file_size"`
	Checksum         string    `json:"checksum"`
	CompletedAt      time.Time `json:"completed_at"`
}

type RecordContext struct {
	Schema       string `json:"schema"`
	RecordTaskID int    `json:"record_task_id"`
	MatchRoundID int    `json:"match_round_id"`
	Role         string `json:"role"`
	SourceURL    string `json:"source_url"`
	OutputPath   string `json:"output_path"`
	BaseDir      string `json:"base_dir"`
	KeepAudio    bool   `json:"keep_audio"`
}

type RecordResult struct {
	Schema       string    `json:"schema"`
	RecordTaskID int       `json:"record_task_id"`
	OutputPath   string    `json:"output_path"`
	Format       string    `json:"format"`
	Codec        string    `json:"codec"`
	FileSize     int64     `json:"file_size"`
	Checksum     string    `json:"checksum"`
	CompletedAt  time.Time `json:"completed_at"`
}

type UploadContext struct {
	Schema              string `json:"schema"`
	UploadTaskID        int    `json:"upload_task_id"`
	SourcePath          string `json:"source_path"`
	BaseDir             string `json:"base_dir"`
	BitableAppToken     string `json:"bitable_app_token"`
	BitableTableID      string `json:"bitable_table_id"`
	BitableRecordID     string `json:"bitable_record_id"`
	BitableRecordURL    string `json:"bitable_record_url,omitempty"`
	AttachmentFieldName string `json:"attachment_field_name"`
}

type UploadResult struct {
	Schema              string    `json:"schema"`
	UploadTaskID        int       `json:"upload_task_id"`
	AttachmentFileToken string    `json:"attachment_file_token"`
	BitableRecordURL    string    `json:"bitable_record_url,omitempty"`
	FileSize            int64     `json:"file_size"`
	CompletedAt         time.Time `json:"completed_at"`
}

type STTContext struct {
	Schema           string `json:"schema"`
	MatchRoundID     int    `json:"match_round_id"`
	MatchID          string `json:"match_id"`
	RoundNo          int    `json:"round_no"`
	Role             string `json:"role"`
	SourceURL        string `json:"source_url"`
	RoundDir         string `json:"round_dir"`
	AudioDir         string `json:"audio_dir"`
	STTPath          string `json:"stt_path"`
	SubtitleName     string `json:"subtitle_name"`
	Prompt           string `json:"prompt"`
	WhisperServerURL string `json:"whisper_server_url"`
}

type STTResult struct {
	Schema       string    `json:"schema"`
	MatchRoundID int       `json:"match_round_id"`
	STTPath      string    `json:"stt_path"`
	CompletedAt  time.Time `json:"completed_at"`
}

type DanmuContext struct {
	Schema       string    `json:"schema"`
	MatchRoundID int       `json:"match_round_id"`
	ChatRoomID   string    `json:"chat_room_id"`
	RoundDir     string    `json:"round_dir"`
	StartedAt    time.Time `json:"started_at"`
	EndedAt      time.Time `json:"ended_at,omitempty"`
}

type DanmuResult struct {
	Schema       string    `json:"schema"`
	MatchRoundID int       `json:"match_round_id"`
	OutputPath   string    `json:"output_path"`
	CompletedAt  time.Time `json:"completed_at"`
}

func ContextFromEnv(v any) error {
	raw := os.Getenv(EnvName)
	if raw == "" {
		return errors.Errorf("%s is required", EnvName)
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return errors.Wrap(err, "decode job context")
	}
	return nil
}

func WriteContext(dir string, v any) error {
	return AtomicWriteJSON(filepath.Join(dir, ContextFile), v)
}

func WriteResult(dir string, v any) error {
	return AtomicWriteJSON(filepath.Join(dir, ResultFile), v)
}

func WriteError(dir, taskType string, taskID int, err error) error {
	msg := ""
	if err != nil {
		msg = Tail(err.Error(), 4096)
	}
	return AtomicWriteJSON(filepath.Join(dir, ErrorFile), ErrorResult{
		Schema:       "rm-monitor/job-error/v1",
		TaskType:     taskType,
		TaskID:       taskID,
		Status:       "FAILED",
		ErrorMessage: msg,
		CompletedAt:  time.Now(),
	})
}

func ReadJSON(path string, v any) (bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, errors.Wrap(err, "read json")
	}
	if err := json.Unmarshal(data, v); err != nil {
		return true, errors.Wrap(err, "decode json")
	}
	return true, nil
}

func Clear(dir string) error {
	for _, name := range []string{ContextFile, ResultFile, ErrorFile, ContextFile + ".tmp", ResultFile + ".tmp", ErrorFile + ".tmp"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return errors.Wrapf(err, "remove %s", name)
		}
	}
	return nil
}

func AtomicWriteJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errors.Wrap(err, "create json dir")
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errors.Wrap(err, "encode json")
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return errors.Wrap(err, "write json tmp")
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return errors.Wrap(err, "publish json")
	}
	return nil
}

func Tail(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}

package jobcontract

import (
	"encoding/json"
	stderrors "errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/pkg/errors"
)

const (
	EnvName     = "RM_MONITOR_JOB_CONTEXT"
	DirName     = ".job"
	ContextFile = "context.json"
	ResultFile  = "result.json"
	ErrorFile   = "error.json"
	TempJobDir  = "/tmp/job"
	ArgoOutDir  = "/tmp/argo"
)

const (
	ExitFailed    = 1
	ExitContract  = 2
	ExitTemporary = 75
)

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var netErr net.Error
	if stderrors.As(err, &netErr) {
		return ExitTemporary
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"timeout", "temporarily unavailable", "connection reset", "connection refused", "broken pipe", "too many requests", "status 429", "status 500", "status 502", "status 503", "status 504", "database is locked", "i/o timeout"} {
		if strings.Contains(message, marker) {
			return ExitTemporary
		}
	}
	return ExitFailed
}

type ErrorResult struct {
	Schema       string    `json:"schema"`
	TaskType     string    `json:"task_type"`
	Status       string    `json:"status"`
	ErrorMessage string    `json:"error_message"`
	CompletedAt  time.Time `json:"completed_at"`
}

type TranscodeContext struct {
	Schema              string   `json:"schema"`
	MatchID             string   `json:"match_id,omitempty"`
	MatchRoundID        int      `json:"match_round_id,omitempty"`
	RoundNo             int      `json:"round_no,omitempty"`
	SourcePath          string   `json:"source_path"`
	ArchivePath         string   `json:"archive_path"`
	BaseDir             string   `json:"base_dir"`
	SourceRetentionDays int      `json:"source_retention_days"`
	RoundDir            string   `json:"round_dir,omitempty"`
	Role                string   `json:"role,omitempty"`
	TrimStartSeconds    *float64 `json:"trim_start_seconds,omitempty"`
	TrimEndSeconds      *float64 `json:"trim_end_seconds,omitempty"`
}

type TranscodeResult struct {
	Schema       string    `json:"schema"`
	MatchID      string    `json:"match_id,omitempty"`
	MatchRoundID int       `json:"match_round_id,omitempty"`
	ArchivePath  string    `json:"archive_path"`
	Format       string    `json:"format"`
	Codec        string    `json:"codec"`
	FileSize     int64     `json:"file_size"`
	Checksum     string    `json:"checksum"`
	CompletedAt  time.Time `json:"completed_at"`
}

type RecordContext struct {
	Schema       string `json:"schema"`
	MatchID      string `json:"match_id,omitempty"`
	MatchRoundID int    `json:"match_round_id"`
	RoundNo      int    `json:"round_no,omitempty"`
	Role         string `json:"role"`
	SourceURL    string `json:"source_url"`
	OutputPath   string `json:"output_path"`
	BaseDir      string `json:"base_dir"`
	KeepAudio    bool   `json:"keep_audio"`
}

type RecordResult struct {
	Schema       string    `json:"schema"`
	MatchID      string    `json:"match_id,omitempty"`
	MatchRoundID int       `json:"match_round_id,omitempty"`
	OutputPath   string    `json:"output_path"`
	Format       string    `json:"format"`
	Codec        string    `json:"codec"`
	FileSize     int64     `json:"file_size"`
	Checksum     string    `json:"checksum"`
	CompletedAt  time.Time `json:"completed_at"`
}

type RecordTrimContext struct {
	Schema       string `json:"schema"`
	MatchID      string `json:"match_id,omitempty"`
	MatchRoundID int    `json:"match_round_id"`
	RoundNo      int    `json:"round_no,omitempty"`
	Role         string `json:"role"`
	BaseDir      string `json:"base_dir"`
	RoundDir     string `json:"round_dir"`
	SourcePath   string `json:"source_path"`
	OutputPath   string `json:"output_path"`
	TrimSidecars bool   `json:"trim_sidecars"`
}

type RecordTrimResult struct {
	Schema          string    `json:"schema"`
	MatchID         string    `json:"match_id,omitempty"`
	MatchRoundID    int       `json:"match_round_id,omitempty"`
	Role            string    `json:"role"`
	OutputPath      string    `json:"output_path"`
	Format          string    `json:"format"`
	Codec           string    `json:"codec"`
	FileSize        int64     `json:"file_size"`
	Checksum        string    `json:"checksum"`
	StartSeconds    float64   `json:"start_seconds"`
	EndSeconds      float64   `json:"end_seconds"`
	DurationSeconds float64   `json:"duration_seconds"`
	CompletedAt     time.Time `json:"completed_at"`
}

type LarkRecordContext struct {
	Schema              string         `json:"schema"`
	MatchID             string         `json:"match_id"`
	MatchRoundID        int            `json:"match_round_id"`
	RoundNo             int            `json:"round_no"`
	Role                string         `json:"role"`
	SourcePath          string         `json:"source_path"`
	BaseDir             string         `json:"base_dir"`
	BitableAppToken     string         `json:"bitable_app_token"`
	BitableTableIDHint  string         `json:"bitable_table_id_hint,omitempty"`
	BitableTableName    string         `json:"bitable_table_name"`
	BitableRecordIDHint string         `json:"bitable_record_id_hint,omitempty"`
	BitableRecordURL    string         `json:"bitable_record_url,omitempty"`
	AttachmentFieldName string         `json:"attachment_field_name"`
	RecordFields        map[string]any `json:"record_fields"`
}

type LarkRecordResult struct {
	Schema              string    `json:"schema"`
	MatchID             string    `json:"match_id"`
	MatchRoundID        int       `json:"match_round_id"`
	Role                string    `json:"role"`
	BitableAppToken     string    `json:"bitable_app_token"`
	BitableTableID      string    `json:"bitable_table_id"`
	BitableRecordID     string    `json:"bitable_record_id"`
	AttachmentFileToken string    `json:"attachment_file_token"`
	BitableRecordURL    string    `json:"bitable_record_url,omitempty"`
	FileSize            int64     `json:"file_size"`
	CompletedAt         time.Time `json:"completed_at"`
}

type STTContext struct {
	Schema            string   `json:"schema"`
	MatchRoundID      int      `json:"match_round_id"`
	MatchID           string   `json:"match_id"`
	RoundNo           int      `json:"round_no"`
	Role              string   `json:"role"`
	SourcePath        string   `json:"source_path"`
	RoundDir          string   `json:"round_dir"`
	STTPath           string   `json:"stt_path"`
	SubtitleName      string   `json:"subtitle_name"`
	Prompt            string   `json:"prompt"`
	WhisperServerURLs []string `json:"whisper_server_urls"`
	TrimStartSeconds  *float64 `json:"trim_start_seconds,omitempty"`
	TrimEndSeconds    *float64 `json:"trim_end_seconds,omitempty"`
}

type STTResult struct {
	Schema       string    `json:"schema"`
	MatchRoundID int       `json:"match_round_id"`
	STTPath      string    `json:"stt_path"`
	SubtitlePath string    `json:"subtitle_path,omitempty"`
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

type AnalyzeScanContext struct {
	FPS                           float64 `json:"fps"`
	Width                         int     `json:"width"`
	Height                        int     `json:"height"`
	MaxStartScanSeconds           int     `json:"max_start_scan_seconds"`
	MaxSettlementScanSeconds      int     `json:"max_settlement_scan_seconds"`
	SettlementChunkSeconds        int     `json:"settlement_chunk_seconds"`
	MinSettlementSecond           int     `json:"min_settlement_second"`
	MinRoundSeconds               int     `json:"min_round_seconds"`
	SettlementTailSeconds         int     `json:"settlement_tail_seconds"`
	SettlementRefineWindowSeconds int     `json:"settlement_refine_window_seconds"`
	SettlementRefineFPS           float64 `json:"settlement_refine_fps"`
}

type AnalyzeContext struct {
	Schema            string             `json:"schema"`
	MatchRoundID      int                `json:"match_round_id"`
	SourcePath        string             `json:"source_path"`
	RoundDir          string             `json:"round_dir"`
	Role              string             `json:"role"`
	OCRServerURL      string             `json:"ocr_server_url,omitempty"`
	OCRTimeoutSeconds int                `json:"ocr_timeout_seconds,omitempty"`
	Scan              AnalyzeScanContext `json:"scan"`
}

type AnalyzeResult struct {
	Schema                string    `json:"schema"`
	MatchRoundID          int       `json:"match_round_id"`
	RoundJSONPath         string    `json:"round_json_path"`
	SettlementImagePath   string    `json:"settlement_image_path,omitempty"`
	SettlementStatus      string    `json:"settlement_status"`
	EffectiveStartSeconds float64   `json:"effective_start_seconds"`
	EffectiveEndSeconds   float64   `json:"effective_end_seconds"`
	CompletedAt           time.Time `json:"completed_at"`
}

func ContextFromEnv(v any) error {
	raw := os.Getenv(EnvName)
	if raw == "" {
		return errors.Errorf("%s is required", EnvName)
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return errors.Wrap(err, "decode job context")
	}
	return validateContext(v)
}

func validateContext(v any) error {
	required := func(schema, expected string, fields map[string]bool) error {
		if schema != expected || !strings.HasPrefix(schema, "rm-monitor/") || !strings.HasSuffix(schema, "/v1") {
			return errors.Errorf("invalid context schema %q, expected %q", schema, expected)
		}
		for name, ok := range fields {
			if !ok {
				return errors.Errorf("context field %s is required", name)
			}
		}
		return nil
	}
	switch c := v.(type) {
	case *TranscodeContext:
		return required(c.Schema, "rm-monitor/transcode-context/v1", map[string]bool{"source_path": c.SourcePath != "", "archive_path": c.ArchivePath != "", "base_dir": c.BaseDir != ""})
	case *RecordContext:
		return required(c.Schema, "rm-monitor/record-context/v1", map[string]bool{"match_round_id": c.MatchRoundID > 0, "role": c.Role != "", "source_url": c.SourceURL != "", "output_path": c.OutputPath != "", "base_dir": c.BaseDir != ""})
	case *RecordTrimContext:
		return required(c.Schema, "rm-monitor/record-trim-context/v1", map[string]bool{"match_round_id": c.MatchRoundID > 0, "role": c.Role != "", "source_path": c.SourcePath != "", "output_path": c.OutputPath != ""})
	case *LarkRecordContext:
		return required(c.Schema, "rm-monitor/lark-record-context/v1", map[string]bool{"match_id": c.MatchID != "", "match_round_id": c.MatchRoundID > 0, "role": c.Role != "", "source_path": c.SourcePath != "", "bitable_app_token": c.BitableAppToken != ""})
	case *STTContext:
		return required(c.Schema, "rm-monitor/stt-context/v1", map[string]bool{"match_round_id": c.MatchRoundID > 0, "source_path": c.SourcePath != "", "round_dir": c.RoundDir != "", "stt_path": c.STTPath != "", "whisper_server_urls": len(c.WhisperServerURLs) > 0})
	case *DanmuContext:
		return required(c.Schema, "rm-monitor/danmu-context/v1", map[string]bool{"match_round_id": c.MatchRoundID > 0, "chat_room_id": c.ChatRoomID != "", "round_dir": c.RoundDir != ""})
	case *AnalyzeContext:
		return required(c.Schema, "rm-monitor/analyze-context/v1", map[string]bool{"match_round_id": c.MatchRoundID > 0, "source_path": c.SourcePath != "", "round_dir": c.RoundDir != "", "role": c.Role != ""})
	default:
		value := reflect.ValueOf(v)
		if value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
			return errors.Errorf("job context must be a non-nil struct pointer, got %T", v)
		}
		field := value.Elem().FieldByName("Schema")
		if !field.IsValid() || field.Kind() != reflect.String {
			return errors.Errorf("context type %T must define a schema field", v)
		}
		schema := field.String()
		if !strings.HasPrefix(schema, "rm-monitor/") || !strings.HasSuffix(schema, "/v1") {
			return errors.Errorf("invalid context schema %q", schema)
		}
		return nil
	}
}

func WriteContext(_ string, v any) error {
	return AtomicWriteJSON(filepath.Join(TempJobDir, ContextFile), v)
}

func WriteResult(_ string, v any) error {
	return AtomicWriteJSON(filepath.Join(TempJobDir, ResultFile), v)
}

func WriteTempResult(v any) error {
	return AtomicWriteJSON(filepath.Join(TempJobDir, ResultFile), v)
}

func WriteArgoOutputs(values map[string]any) error {
	for name, value := range values {
		if err := writeArgoOutput(name, value); err != nil {
			return err
		}
	}
	return nil
}

func writeArgoOutput(name string, value any) error {
	if err := os.MkdirAll(ArgoOutDir, 0o755); err != nil {
		return errors.Wrap(err, "create argo output dir")
	}
	var raw []byte
	switch v := value.(type) {
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return errors.Wrapf(err, "encode argo output %s", name)
		}
		raw = b
	}
	return os.WriteFile(filepath.Join(ArgoOutDir, name), raw, 0o644)
}

func WriteError(_ string, taskType string, _ int, err error) error {
	msg := ""
	if err != nil {
		msg = Tail(err.Error(), 4096)
	}
	return AtomicWriteJSON(filepath.Join(TempJobDir, ErrorFile), ErrorResult{
		Schema:       "rm-monitor/job-error/v1",
		TaskType:     taskType,
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

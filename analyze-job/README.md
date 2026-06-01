# analyze-job

Round analyzer for RoboMaster main-view recordings.

Responsibilities:

- detect the effective round start from the pregame/self-check layout
- detect the settlement panel from the end of the video
- write `round.json`
- extract source-resolution `settlement.jpg`
- call the PP-OCR daemon and embed settlement OCR into `round.json`
- report job completion through `.job/<job-name>/result.json` or `.job/<job-name>/error.json`

The job accepts the shared job contract through `RM_MONITOR_JOB_CONTEXT`:

```json
{
  "analyze_task_id": 123,
  "match_round_id": 456,
  "source_artifact_id": 789,
  "source_path": "/records/.../Round-1/主视角.flv",
  "round_dir": "/records/.../Round-1",
  "role": "主视角",
  "ocr_server_url": "http://ppocr:8000",
  "scan": {
    "fps": 1,
    "width": 320,
    "height": 180,
    "settlement_chunk_seconds": 90,
    "min_round_seconds": 60
  }
}
```

## Configuration Shape

The OCR daemon is a shared infrastructure service. Application code should only
need the service endpoint and timeout:

```yaml
OCRServerConf:
  BaseURL: http://ppocr-server.rm-monitor.svc.cluster.local:8000
  TimeoutSeconds: 30
```

Analyze scheduling and scan behavior belong to the analyzer feature:

```yaml
AnalyzeConf:
  Enabled: false
  Role: "主视角"
  MaxConcurrentJobs: 1
  Scan:
    FPS: 1
    Width: 320
    Height: 180
    SettlementChunkSeconds: 90
    MinSettlementSecond: 60
    MinRoundSeconds: 60
    SettlementTailSeconds: 5
```

Deployment-only OCR server details such as image, replica count, CPU/GPU mode,
resources, and model cache PVC must stay in Helm values, not in app config.

Helm delivery uses the `ocrServer` values block and creates an internal
`ocr-server` Service when enabled. The analyzer should consume it as:

```yaml
OCRServerConf:
  BaseURL: http://ocr-server.rm-monitor.svc.cluster.local:8000
  TimeoutSeconds: 30
```

Local sample evaluation:

```powershell
cd analyze-job\ocr-daemon
docker compose up -d --build

cd ..
python eval_samples.py --ocr-server-url http://127.0.0.1:48089
```

Evaluation output is written to `debug/eval/`.

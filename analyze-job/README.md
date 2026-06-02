# analyze-job

Round analyzer for RoboMaster main-view recordings.

Responsibilities:

- detect the effective round start from the pregame/self-check layout
- detect the settlement panel from the end of the video
- write `round.json`
- extract source-resolution `settlement.jpg`
- call the external PaddleX OCR service and embed settlement OCR into `round.json`
- report job completion through `/tmp/job/result.json` or `/tmp/job/error.json`, plus Argo output parameters under `/tmp/argo`

The job accepts the shared job contract through `RM_MONITOR_JOB_CONTEXT`:

```json
{
  "match_round_id": 456,
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

The OCR service is shared infrastructure. Application code only needs the
service base URL and timeout:

```yaml
OCRServerConf:
  BaseURL: http://host.docker.internal:48089
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

Deployment-only OCR server details such as image, GPU runtime, model cache, and
PaddleX high-performance serving files belong to the external OCR deployment,
not this chart. Helm only passes the OCR service base URL into jobs:

```yaml
ocrServer:
  baseURL: http://host.docker.internal:48089
  timeoutSeconds: 30
```

## PaddleX HPS OCR Service

The analyzer consumes the official PaddleX high-performance serving API:

- service root: `OCRServerConf.BaseURL`, for example `http://172.30.162.40:48089`
- endpoint used by analyze-job: `/v2/models/ocr/infer`
- request body: PaddleX/Triton JSON with base64 image input
- response body: `outputs[0].data[0]` containing PaddleX OCR JSON

Recommended deployment is the official PaddleX HPS SDK flow:

1. Open the PaddleX serving SDK guide:
   https://paddlepaddle.github.io/PaddleX/latest/pipeline_deploy/serving.html#21-sdk
2. Download the **OCR** SDK package, `paddlex_hps_OCR_sdk.tar.gz`.
3. Unpack its `server` directory into `analyze-job/ocr-server/server`.
4. Start the service with the checked-in compose file:

```powershell
cd analyze-job\ocr-server
docker compose up -d
```

The compose file is intentionally small and uses the official HPS GPU image:

```yaml
services:
  ppocr:
    image: ccr-2vdh3abv-pub.cnc.bj.baidubce.com/paddlex/hps:paddlex3.5-gpu
    container_name: ppocr-server
    restart: unless-stopped
    shm_size: 8g
    ports:
      - "48089:8080"
    volumes:
      - ./server:/app
      - ./logs:/logs
    environment:
      PADDLEX_HPS_USE_HPIP: "1"
      NVIDIA_VISIBLE_DEVICES: all
      NVIDIA_DRIVER_CAPABILITIES: compute,utility
    entrypoint:
      - bash
      - /app/server.sh
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: 1
              capabilities: [gpu]
```

The OCR service is fixed to Chinese PP-OCRv5 GPU serving through the official
OCR SDK package. Do not build or ship a custom rm-monitor OCR server image.

Health check:

```powershell
curl http://127.0.0.1:48089/v2/health/ready
```

Local sample evaluation:

```powershell
# after starting the official PaddleX OCR service on 127.0.0.1:48089

python eval_samples.py --ocr-server-url http://127.0.0.1:48089
```

Evaluation output is written to `debug/eval/`.

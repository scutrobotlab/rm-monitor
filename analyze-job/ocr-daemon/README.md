# PP-OCRv5 daemon

This is the shared OCR service used by `analyze-job`. It is intentionally
generic: it OCRs uploaded ROI images and returns text boxes. `analyze-job`
owns video scanning, ROI extraction, field validation, and embedding settlement data into `round.json`.

## Local CPU

```powershell
cd analyze-job\ocr-daemon
docker compose up -d --build
```

The image currently pins PaddleOCR/PaddleX/PaddlePaddle to compatible 3.0.x
versions. First startup downloads PP-OCRv5 det/rec models into `./models`;
later starts reuse that cache.

## Local GPU

Use the overlay when the host has NVIDIA Container Toolkit configured:

```powershell
cd analyze-job\ocr-daemon
docker compose -f docker-compose.yaml -f docker-compose.gpu.yaml up -d --build
```

The base compose file is CPU-first because it is the safest local development
path. GPU only changes the build arg and runtime env; the HTTP API stays the
same.

## API

```powershell
curl http://127.0.0.1:48089/health
curl -F "files=@..\debug\roi-reference-overlay\sample10_ref\crops\blue_outpost_hp.jpg" http://127.0.0.1:48089/ocr
```

`POST /ocr` accepts multipart field `files` and returns:

```json
{
  "results": [
    {
      "name": "red_damage.jpg",
      "text": "2921",
      "items": [
        { "text": "2921", "confidence": 0.99, "box": [[0, 0], [1, 0], [1, 1], [0, 1]] }
      ]
    }
  ],
  "elapsed_seconds": 0.12
}
```

## Delivery Shape

Production should deploy this as a long-running `ppocr-server` Deployment:

- no PG, Redis, records PVC, or live API access
- only a model cache volume, preferably a small PVC mounted at `/root/.paddlex`
- one replica by default; scale only after measuring GPU/CPU saturation
- internal Service URL consumed by `OCRServerConf.BaseURL`

Runtime knobs:

| Env | Default | Notes |
| --- | --- | --- |
| `OCR_DEVICE` | `cpu` | `cpu` or `gpu:0` |
| `OCR_LANG` | `ch` | PaddleOCR language |
| `OCR_VERSION` | `PP-OCRv5` | model version |
| `OCR_ENABLE_MKLDNN` | `true` | CPU optimization; disable for GPU |
| `OCR_CPU_THREADS` | `4` | CPU inference threads |
| `OCR_MAX_BATCH` | `32` | max multipart files per request |

Recommended app config:

```yaml
OCRServerConf:
  BaseURL: http://ppocr-server.rm-monitor.svc.cluster.local:8000
  TimeoutSeconds: 30
```

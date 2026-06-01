# Argo local E2E

`argo-local.ps1` runs a Docker Desktop Kubernetes E2E for the Argo-based rm-monitor workflow.

The test keeps only the match schedule/live-info control plane mocked. AI and recognition services are real external components:

- Dify: `RM_MONITOR_E2E_DIFY_BASE_URL`, default `https://dify.scutbot.cn`; do not include `/v1`
- Manifest workflow key: `RM_MONITOR_E2E_MANIFEST_WORKFLOW_API_KEY`, required
- Whisper server: `RM_MONITOR_E2E_WHISPER_URL`, default `http://host.docker.internal:48088/inference`; this must be the exact whisper.cpp `/inference` endpoint
- OCR server: `RM_MONITOR_E2E_OCR_URL`, default `http://host.docker.internal:48089`
- STT quality workflow key: `RM_MONITOR_E2E_STT_QUALITY_WORKFLOW_API_KEY`, required only with `-EnableSTTQuality`

Example:

```powershell
$env:RM_MONITOR_E2E_MANIFEST_WORKFLOW_API_KEY = "app-..."
$env:RM_MONITOR_E2E_WHISPER_URL = "http://host.docker.internal:8088/inference"
$env:RM_MONITOR_E2E_OCR_URL = "http://host.docker.internal:48089"
.\scripts\e2e\argo-local.ps1 -SkipArgoInstall -PortForwardArgo
```

Optional external writes:

- `-EnableLarkUpload` uses `RM_MONITOR_E2E_LARK_APP_ID`, `RM_MONITOR_E2E_LARK_APP_SECRET`, and `RM_MONITOR_E2E_BITABLE_APP_TOKEN`, or the local private values file.
- `-EnableLiveDanmu` uses `RM_MONITOR_E2E_DANMU_APP_ID` and `RM_MONITOR_E2E_DANMU_APP_KEY`, or production values for read-only LeanCloud access.

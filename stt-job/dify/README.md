# STT Quality Workflow

Import `stt-quality-workflow.yml` into Dify as a Workflow app.

Runtime contract:

- Input variable: `payload`
- API endpoint: `POST /v1/workflows/run`
- Required output: `cleaned_json`

`stt-job` writes raw whisper output to `stt.jsonl` first. When `STTQualityConf.UseQuality=true`, it calls this workflow; only after a successful response does it rename the original file to `stt.raw.jsonl` and write cleaned data back to `stt.jsonl`.

Configure the workflow API key in Helm:

```yaml
stt:
  useQuality: true
  qualityWorkflowAPIKey: app-...
```

Do not store Dify console cookies, provider keys, or workflow API keys in this directory.

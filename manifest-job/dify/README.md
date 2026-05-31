# Dify Manifest Report Workflow

This directory contains the Dify workflow used by `manifest-job` to generate match reports.

## Import

1. Open Dify Studio.
2. Import `report-workflow.yml` as a Workflow app.
3. Configure the LLM node with the production model provider and model.
4. Publish the workflow.
5. Copy the workflow API key from Access Info into:

```yaml
ManifestConf:
  ReportWorkflowAPIKey: "<workflow-api-key>"
```

The global Dify service config stays separate:

```yaml
DifyConf:
  BaseURL: "http://dify-api:5001"
  TimeoutSeconds: 180
```

## Contract

`manifest-job` calls `POST /v1/workflows/run` with one input variable:

```json
{
  "payload": "{\"schema\":\"rm-monitor/dify-report-input/v1\",...}"
}
```

The workflow must output:

- `report_markdown`: Markdown report body written to `matches.report`.
- `report_json`: structured JSON written to `report.json` in the match directory.

The workflow must not mention internal implementation terms such as AI, STT, Dify, system, workflow, or automatic generation in `report_markdown`.

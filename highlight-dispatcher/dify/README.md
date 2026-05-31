# rm-monitor Highlight Review Workflow

This Dify workflow reviews highlight candidates produced by `highlight-dispatcher`.

## Import

1. Import `highlight-review-workflow.yml` into Dify as a Workflow app.
2. Confirm the LLM provider and model are available in your Dify instance.
3. Confirm the two RoboMaster knowledge datasets are connected:
   - RoboMaster rules knowledge base
   - Team/event knowledge base
4. Copy the workflow API key into Helm values:

```yaml
dify:
  baseURL: http://dify-api.dify.svc.cluster.local:5001

highlight:
  enabled: true
  reviewWorkflowAPIKey: app-...
```

## Contract

`highlight-dispatcher` calls `/v1/workflows/run` with one input variable:

```json
{
  "payload": "{\"schema\":\"rm-monitor/dify-highlight-review-input/v1\",...}"
}
```

The workflow must return `highlight_review_json`:

```json
{
  "accepted": true,
  "confidence": 0.82,
  "highlight_type": "团战爆发",
  "title": "...",
  "description": "...",
  "tags": ["RoboMaster", "赛事高光"],
  "reason": "...",
  "publish_caption": "..."
}
```

`accepted=false` means the candidate is marked `SKIPPED` and no artifact job is created.

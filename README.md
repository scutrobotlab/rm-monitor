# rm-monitor

rm-monitor is a RoboMaster match monitoring and recording system. It watches the
official schedule/live data, writes authoritative match state to PostgreSQL, and
drives recording, uploading, transcoding, STT, highlights, manifest generation,
and Feishu card updates through Argo Workflows.

## Architecture

The runtime services are:

- `match-controller`: polls schedule/live data, upserts teams/matches/rounds,
  and creates or resumes deterministic Argo match workflows.
- `lark-notifier`: sends internal Feishu match cards, patches cards
  idempotently from PostgreSQL state, and displays Feishu bitable record links.
- `health-checker`: periodic structured-log-only health check CronJob.

Runtime Jobs are created by Argo workflows, not by Helm:

- `record-job`: records one round/role source to FLV.
- `danmu-job`: records one round's LeanCloud chatroom danmu XML and stats.
- `analyze-job`: detects effective round bounds and settlement output.
- `stt-job`: extracts audio from the configured role recording and calls a
  Whisper server.
- `lark-record-job`: creates Feishu bitable records, uploads source FLV
  attachments, and records the business result.
- `transcode-job`: writes MP4/AV1 archive output.
- `highlight-job`: finds highlight candidates and writes highlight business
  rows.
- `highlight-artifact-job`: renders one highlight's file artifacts.
- `manifest-job`: runs once after a match completes, writes `README.md`, and
  saves the Markdown report on the match.

PostgreSQL is authoritative only for business state. Argo owns orchestration and
dependency state. Redis is used for cache and short-lived external API locks or
rate limits.

## Storage Model

The deployment uses a single real data source plus an internal shared view:

- `records`: a deployer-created local RWO PV on the record node. `record-job`
  writes here directly.
- `records-shared`: a deployer-created internal NFS RWX PV exported from the
  `records` data. Read-only services such as manifest, notifier, and health
  checks can mount this shared view.

OpenList is intentionally outside this chart. If deployed, it should mount the
`records` PVC read-only and serve browsing/preview only.

## Helm Deployment

The chart lives at `charts/rm-monitor`.

```powershell
helm lint charts/rm-monitor
helm template rm-monitor charts/rm-monitor --namespace rm-monitor --values charts/rm-monitor/values.yaml
```

The chart does not create a Namespace, PVs, NFS server, or OpenList. Create
storage first, then install the chart:

```powershell
helm upgrade --install rm-monitor charts/rm-monitor `
  --namespace rm-monitor `
  --create-namespace `
  --values path\to\values-prod.yaml
```

Published charts use OCI:

```powershell
helm upgrade --install rm-monitor oci://ghcr.io/scutrobotlab/charts/rm-monitor `
  --version 0.1.<run_number> `
  --namespace rm-monitor `
  --create-namespace `
  --values path\to\values-prod.yaml
```

Images are not configured in values. The chart always uses
`ghcr.io/scutrobotlab/rm-monitor/<component>:<chart appVersion>`, so production
deploys are pinned by the chart package metadata instead of repeated image
fields in every values file. CI builds and pushes every component image with
that tag before publishing the chart, so a published chart never points at a
partially available image set.

## Configuration

Only deployment-specific runtime values belong in `values.yaml`:

- PostgreSQL DSN
- Redis host/password
- Feishu app and bitable token
- Dify base URL and per-job workflow API keys
- LLM base URL, API key, and model for jobs not yet migrated to Dify
- optional STT role and Whisper server URL
- storage PVC binding names
- replicas, resources, and scheduling-independent task limits

Avoid adding service alias layers when a DSN or host can name the real endpoint
directly. Sensitive production values should stay in the private deployment
repository, not in this public source repository.

## Development Checks

```powershell
go test ./...
helm lint charts/rm-monitor
helm template rm-monitor charts/rm-monitor --namespace rm-monitor --values charts/rm-monitor/values.yaml > .local\helm-template-check.yaml
```

For local end-to-end validation, deploy a test values file to `rm-monitor-dev`
with local PostgreSQL/Redis/storage and a reachable Whisper server.

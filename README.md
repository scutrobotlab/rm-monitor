# rm-monitor

rm-monitor is a RoboMaster match monitoring and recording system. It watches the
official schedule/live data, writes authoritative match state to PostgreSQL, and
drives recording, uploading, transcoding, STT, manifest generation, and Feishu
card updates through Kubernetes Jobs.

## Architecture

The runtime services are:

- `monitor`: polls schedule/live data, upserts teams/matches/rounds, and writes
  Redis heartbeat/cache state.
- `lark-notifier`: sends internal Feishu match cards, patches cards
  idempotently from PostgreSQL state, and replies when uploads complete.
- `record-dispatcher`: creates record, STT, and manifest Jobs from PostgreSQL
  state.
- `uploader-dispatcher`: creates upload Jobs for source artifacts.
- `transcode-dispatcher`: creates AV1 archive transcode Jobs.
- `health-checker`: periodic structured-log-only health check CronJob.

Runtime Jobs are created by dispatchers, not by Helm:

- `record-job`: records one round/role source to FLV.
- `stt-job`: records one configured role's audio segments and calls a Whisper
  server.
- `manifest-job`: runs once after a match completes, writes `README.md`, and
  saves the Markdown report on the match.
- `uploader-job`: uploads source FLV to Feishu bitable attachments.
- `transcode-job`: writes MP4/AV1 archive output.
- `artifact-cleaner`: deletes expired source artifacts after archive and upload
  have reached explicit terminal states.

PostgreSQL is the authoritative business state. Redis is only used for cache,
rate limiting, monitor heartbeat, and short-lived STT coordination signals.

## Storage Model

The deployment uses a single real data source plus an internal shared view:

- `records`: a deployer-created local RWO PV on the record node. `record-job`
  writes here directly.
- `records-shared`: a deployer-created internal NFS RWX PV exported from the
  `records` data. Upload, transcode, STT, manifest, health-check, and cleanup
  Jobs mount this PVC so they can drift across nodes.

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
fields in every values file.

## Configuration

Only deployment-specific runtime values belong in `values.yaml`:

- PostgreSQL DSN
- Redis host/password
- Feishu app and bitable token
- LLM base URL, API key, and model
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

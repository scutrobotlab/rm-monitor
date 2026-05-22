# rm-monitor Helm Chart

This chart is the deployment source for rm-monitor. The legacy `k8s/`
kustomize manifests have been removed to avoid maintaining two deployment
models.

## Render

```powershell
helm lint charts/rm-monitor
helm template rm-monitor charts/rm-monitor --namespace rm-monitor --values charts/rm-monitor/values.yaml
```

## Production

Production values live in the internal deployments repository:

```powershell
helm upgrade --install rm-monitor charts/rm-monitor `
  --namespace rm-monitor `
  --create-namespace `
  --values D:\src\kubernetes-deployments\rm-monitor\values-prod.yaml
```

On push, GitHub Actions packages the chart as
`ghcr.io/scutrobotlab/charts/rm-monitor` with version `0.1.<run_number>`.

```powershell
helm upgrade --install rm-monitor oci://ghcr.io/scutrobotlab/charts/rm-monitor `
  --version 0.1.<run_number> `
  --namespace rm-monitor `
  --create-namespace `
  --values D:\src\kubernetes-deployments\rm-monitor\values-prod.yaml
```

The chart does not create a Namespace object. The deployment namespace is chosen
by `helm --namespace`; use `--create-namespace` when the namespace does not
already exist.

The chart also does not create PVs, NFS servers, CSI credentials, or OpenList.
Deployers create storage first, then set the PV names under
`storage.record.volumeName` and `storage.shared.volumeName`; the chart creates
PVCs bound to those names.

Images are intentionally not values. The chart renders
`ghcr.io/scutrobotlab/rm-monitor/<component>:<chart appVersion>` for every
component, so deployments are pinned by chart package metadata rather than a
second image tag field in production values.

The expected production storage topology is:

- `storage.record`: local RWO PV on the record node, used by `record-job`.
- `storage.shared`: internal NFS RWX PV exported from the record PV, used by
  upload/transcode/stt/manifest jobs and artifact cleanup.

OpenList, when deployed, is a separate optional read-only browser over the
record PVC. It is not part of the write path.

The chart includes a lightweight `health-checker` CronJob. It does not send
messages or mutate business state; it only writes structured logs and fails the
CronJob when PG, Redis, monitor heartbeat, task status, failed runtime Jobs, or
the shared records PVC are unhealthy.

For a first migration from legacy `kubectl apply` resources, either remove the
old non-data resources and reinstall with Helm, or do a one-off manual adoption.
Do not keep ownership-adoption flags in the regular deployment command.

Dynamic record/upload/transcode/stt/manifest Job instances are still created by
the dispatchers at runtime. Helm only manages the long-running services,
configuration, RBAC, storage PVCs, and artifact-cleaner CronJob.

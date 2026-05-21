#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
NS="${E2E_NAMESPACE:-rm-monitor-e2e}"
RELEASE="${E2E_RELEASE:-rm-monitor-e2e}"
CLUSTER="${E2E_KIND_CLUSTER:-rm-monitor-e2e}"
USE_KIND="${E2E_USE_KIND:-0}"
BUILD_IMAGES="${E2E_BUILD_IMAGES:-0}"
KEEP="${E2E_KEEP:-0}"
IMAGE_TAG="dev"
KUBECTL="${KUBECTL:-kubectl}"
HELM="${HELM:-helm}"
KIND="${KIND:-kind}"

if ! command -v "$KUBECTL" >/dev/null 2>&1 && command -v kubectl.exe >/dev/null 2>&1; then
  KUBECTL="kubectl.exe"
fi
if ! command -v "$HELM" >/dev/null 2>&1 && command -v helm.exe >/dev/null 2>&1; then
  HELM="helm.exe"
fi
if ! command -v "$KIND" >/dev/null 2>&1 && command -v kind.exe >/dev/null 2>&1; then
  KIND="kind.exe"
fi
log_tools_once=0

host_path() {
  local p="$1"
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -w "$p"
  elif command -v wslpath >/dev/null 2>&1 && [[ "$p" == /mnt/* ]]; then
    wslpath -w "$p"
  else
    printf '%s\n' "$p"
  fi
}

apps=(monitor record-dispatcher record-job)

log() { printf '[e2e] %s\n' "$*" >&2; }

cleanup() {
  if [[ "$KEEP" == "1" ]]; then
    log "keeping namespace/cluster for inspection"
    return
  fi
  "$KUBECTL" delete namespace "$NS" --ignore-not-found=true >/dev/null 2>&1 || true
  "$KUBECTL" delete pv e2e-records e2e-records-shared --ignore-not-found=true >/dev/null 2>&1 || true
  if [[ "$USE_KIND" == "1" ]]; then
    "$KIND" delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

if [[ "$USE_KIND" == "1" ]]; then
  if ! "$KIND" get clusters | grep -qx "$CLUSTER"; then
    log "creating kind cluster $CLUSTER"
    "$KIND" create cluster --name "$CLUSTER" --wait 120s
  fi
fi

if [[ "$BUILD_IMAGES" == "1" ]]; then
  for app in "${apps[@]}"; do
    image="ghcr.io/scutrobotlab/rm-monitor/${app}:${IMAGE_TAG}"
    log "building $image"
    docker build -f "$ROOT/${app}/Dockerfile" -t "$image" "$ROOT"
    if [[ "$USE_KIND" == "1" ]]; then
      log "loading $image into kind"
      "$KIND" load docker-image "$image" --name "$CLUSTER"
    fi
  done
fi

log "preparing namespace and storage"
"$KUBECTL" delete namespace "$NS" --ignore-not-found=true >/dev/null 2>&1 || true
"$KUBECTL" wait --for=delete namespace/"$NS" --timeout=60s >/dev/null 2>&1 || true
"$KUBECTL" create namespace "$NS"

"$KUBECTL" delete pv e2e-records e2e-records-shared --ignore-not-found=true >/dev/null 2>&1 || true
cat <<'YAML' | "$KUBECTL" apply -f -
apiVersion: v1
kind: PersistentVolume
metadata:
  name: e2e-records
spec:
  capacity:
    storage: 2Gi
  accessModes: ["ReadWriteOnce"]
  persistentVolumeReclaimPolicy: Delete
  storageClassName: e2e-records
  hostPath:
    path: /tmp/rm-monitor-e2e-records
    type: DirectoryOrCreate
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: e2e-records-shared
spec:
  capacity:
    storage: 2Gi
  accessModes: ["ReadWriteMany"]
  persistentVolumeReclaimPolicy: Delete
  storageClassName: e2e-records-shared
  hostPath:
    path: /tmp/rm-monitor-e2e-records
    type: DirectoryOrCreate
YAML

log "deploying postgres, redis, mock server and live media"
"$KUBECTL" -n "$NS" create configmap e2e-mock-server --from-file=mock-server.py="$(host_path "$ROOT/scripts/e2e/mock-server.py")"
cat <<'YAML' | "$KUBECTL" -n "$NS" apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: postgres
spec:
  replicas: 1
  selector:
    matchLabels: {app: postgres}
  template:
    metadata:
      labels: {app: postgres}
    spec:
      containers:
        - name: postgres
          image: postgres:16-alpine
          env:
            - {name: POSTGRES_USER, value: rm_monitor}
            - {name: POSTGRES_PASSWORD, value: rm_monitor}
            - {name: POSTGRES_DB, value: rm_monitor}
          ports:
            - {containerPort: 5432}
---
apiVersion: v1
kind: Service
metadata:
  name: postgres
spec:
  selector: {app: postgres}
  ports:
    - {port: 5432, targetPort: 5432}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis
spec:
  replicas: 1
  selector:
    matchLabels: {app: redis}
  template:
    metadata:
      labels: {app: redis}
    spec:
      containers:
        - name: redis
          image: redis:7-alpine
          ports:
            - {containerPort: 6379}
---
apiVersion: v1
kind: Service
metadata:
  name: redis
spec:
  selector: {app: redis}
  ports:
    - {port: 6379, targetPort: 6379}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: e2e-mock
spec:
  replicas: 1
  selector:
    matchLabels: {app: e2e-mock}
  template:
    metadata:
      labels: {app: e2e-mock}
    spec:
      containers:
        - name: mock
          image: python:3.12-alpine
          command: ["python", "/mock/mock-server.py"]
          ports:
            - {containerPort: 8080}
          volumeMounts:
            - {name: mock, mountPath: /mock, readOnly: true}
      volumes:
        - name: mock
          configMap:
            name: e2e-mock-server
---
apiVersion: v1
kind: Service
metadata:
  name: e2e-mock
spec:
  selector: {app: e2e-mock}
  ports:
    - {port: 8080, targetPort: 8080}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: e2e-media
spec:
  replicas: 1
  selector:
    matchLabels: {app: e2e-media}
  template:
    metadata:
      labels: {app: e2e-media}
    spec:
      containers:
        - name: media
          image: ghcr.io/scutrobotlab/rm-monitor/record-job:dev
          imagePullPolicy: IfNotPresent
          command: ["/bin/sh", "-lc"]
          args:
            - |
              set -eu
              mkdir -p /tmp/hls
              ffmpeg -hide_banner -loglevel error -re \
                -f lavfi -i testsrc2=size=320x180:rate=15 \
                -f lavfi -i anullsrc=channel_layout=mono:sample_rate=44100 \
                -c:v libx264 -preset ultrafast -pix_fmt yuv420p -g 30 \
                -c:a aac -f hls -hls_time 1 -hls_list_size 6 \
                -hls_flags delete_segments+append_list /tmp/hls/main.m3u8 &
              busybox httpd -f -p 8080 -h /tmp/hls
          ports:
            - {containerPort: 8080}
---
apiVersion: v1
kind: Service
metadata:
  name: e2e-media
spec:
  selector: {app: e2e-media}
  ports:
    - {port: 8080, targetPort: 8080}
YAML

"$KUBECTL" -n "$NS" rollout status deploy/postgres --timeout=120s
"$KUBECTL" -n "$NS" rollout status deploy/redis --timeout=120s
"$KUBECTL" -n "$NS" rollout status deploy/e2e-mock --timeout=120s
"$KUBECTL" -n "$NS" rollout status deploy/e2e-media --timeout=120s

log "installing rm-monitor chart"
"$HELM" upgrade --install "$RELEASE" "$(host_path "$ROOT/charts/rm-monitor")" \
  --namespace "$NS" \
  --set imagePullPolicy=IfNotPresent \
  --set jobImagePullPolicy=IfNotPresent \
  --set postgres.dsn='postgres://rm_monitor:rm_monitor@postgres:5432/rm_monitor?sslmode=disable' \
  --set redis.host='redis:6379' \
  --set monitor.scheduleURL='http://e2e-mock:8080/schedule.json' \
  --set record.liveInfoURL='http://e2e-mock:8080/live_game_info.json' \
  --set record.res='1080p' \
  --set storage.record.storageClassName='e2e-records' \
  --set storage.record.volumeName='e2e-records' \
  --set storage.shared.storageClassName='e2e-records-shared' \
  --set storage.shared.volumeName='e2e-records-shared' \
  --set components.larkNotifier.replicas=0 \
  --set components.uploaderDispatcher.replicas=0 \
  --set components.transcodeDispatcher.replicas=0 \
  --set components.highlightDispatcher.replicas=0

"$KUBECTL" -n "$NS" rollout status deploy/monitor --timeout=120s
"$KUBECTL" -n "$NS" rollout status deploy/record-dispatcher --timeout=120s

mock_set() {
  local query="$1"
  "$KUBECTL" -n "$NS" run e2e-curl --rm -i --restart=Never --image=curlimages/curl:8.10.1 -- \
    -fsS "http://e2e-mock:8080/set?${query}" >/dev/null
}

psql_value() {
  local sql="$1"
  "$KUBECTL" -n "$NS" exec deploy/postgres -- env PGPASSWORD=rm_monitor \
    psql -U rm_monitor -d rm_monitor -tAc "$sql" | tr -d '\r[:space:]'
}

wait_sql_ge() {
  local sql="$1"
  local want="$2"
  local label="$3"
  local deadline=$((SECONDS + 180))
  while (( SECONDS < deadline )); do
    local got
    got="$(psql_value "$sql" || true)"
    if [[ "$got" =~ ^[0-9]+$ ]] && (( got >= want )); then
      log "$label: $got"
      return 0
    fi
    sleep 2
  done
  log "timeout waiting for $label"
  "$KUBECTL" -n "$NS" get pods,jobs
  "$KUBECTL" -n "$NS" logs deploy/monitor --tail=100 || true
  "$KUBECTL" -n "$NS" logs deploy/record-dispatcher --tail=100 || true
  return 1
}

log "letting monitor initialize WAITING snapshot"
sleep 5

log "starting mock match"
mock_set 'status=STARTED&red=0&blue=0&result=UNKNOWN'
wait_sql_ge "select count(*) from match_rounds where status='STARTED'" 1 "started rounds"
wait_sql_ge "select count(*) from record_tasks where status in ('RUNNING','CANCEL_REQUESTED','SUCCEEDED')" 1 "record tasks started"

sleep 8
log "ending mock match"
mock_set 'status=DONE&red=1&blue=0&result=RED'
wait_sql_ge "select count(*) from match_rounds where status='ENDED'" 1 "ended rounds"
wait_sql_ge "select count(*) from record_tasks where status='SUCCEEDED'" 1 "record tasks succeeded"
wait_sql_ge "select count(*) from media_artifacts where kind='source' and status='AVAILABLE'" 1 "source artifacts available"

log "checking result file and recording output"
"$KUBECTL" -n "$NS" run e2e-records-check --rm -i --restart=Never --image=busybox:1.36 --overrides='
{
  "spec": {
    "containers": [{
      "name": "e2e-records-check",
      "image": "busybox:1.36",
      "command": ["sh", "-lc", "find /records -name result.json -path \"*/.job/record-*/*\" -print -quit | grep . && find /records -name \"*.flv\" -size +0c -print -quit | grep ."],
      "volumeMounts": [{"name": "records", "mountPath": "/records"}]
    }],
    "volumes": [{"name": "records", "persistentVolumeClaim": {"claimName": "records-shared"}}],
    "restartPolicy": "Never"
  }
}' >/dev/null

log "core cluster e2e passed"

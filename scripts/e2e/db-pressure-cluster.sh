#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
NS="${E2E_NAMESPACE:-rm-monitor-pressure}"
RELEASE="${E2E_RELEASE:-rm-monitor-pressure}"
PROFILE="${E2E_PRESSURE_PROFILE:-baseline}"
IDLE_SECONDS="${E2E_PRESSURE_IDLE_SECONDS:-60}"
CHURN_SECONDS="${E2E_PRESSURE_CHURN_SECONDS:-30}"
BUILD_IMAGES="${E2E_BUILD_IMAGES:-1}"
KEEP="${E2E_KEEP:-0}"
KUBECTL="${KUBECTL:-kubectl}"
HELM="${HELM:-helm}"

if ! command -v "$KUBECTL" >/dev/null 2>&1 && command -v kubectl.exe >/dev/null 2>&1; then
  KUBECTL="kubectl.exe"
fi
if ! command -v "$HELM" >/dev/null 2>&1 && command -v helm.exe >/dev/null 2>&1; then
  HELM="helm.exe"
fi

apps=(migrate-job monitor record-dispatcher record-job)

log() { printf '[pressure] %s\n' "$*" >&2; }

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

cleanup() {
  if [[ "$KEEP" == "1" ]]; then
    log "keeping namespace for inspection: $NS"
    return
  fi
  "$KUBECTL" delete namespace "$NS" --ignore-not-found=true >/dev/null 2>&1 || true
  "$KUBECTL" delete pv pressure-records pressure-records-shared --ignore-not-found=true >/dev/null 2>&1 || true
}
trap cleanup EXIT

dump_debug() {
  local exit_code=$?
  if [[ "$exit_code" == "0" ]]; then
    return
  fi
  log "debug dump after failure"
  "$KUBECTL" -n "$NS" get pods,pvc,jobs,events || true
  for name in postgres monitor record-dispatcher e2e-media; do
    "$KUBECTL" -n "$NS" logs deploy/"$name" --tail=160 || true
  done
}
trap dump_debug ERR

current_context="$("$KUBECTL" config current-context)"
if [[ "$current_context" != "docker-desktop" ]]; then
  log "switching kubectl context from $current_context to docker-desktop"
  "$KUBECTL" config use-context docker-desktop >/dev/null
fi

if [[ "$BUILD_IMAGES" == "1" ]]; then
  for app in "${apps[@]}"; do
    image="ghcr.io/scutrobotlab/rm-monitor/${app}:dev"
    log "building $image"
    docker build -f "$ROOT/${app}/Dockerfile" -t "$image" "$ROOT" >/dev/null
  done
fi

log "preparing namespace and storage"
"$KUBECTL" delete namespace "$NS" --ignore-not-found=true >/dev/null 2>&1 || true
"$KUBECTL" wait --for=delete namespace/"$NS" --timeout=60s >/dev/null 2>&1 || true
"$KUBECTL" create namespace "$NS" >/dev/null
"$KUBECTL" delete pv pressure-records pressure-records-shared --ignore-not-found=true >/dev/null 2>&1 || true
cat <<'YAML' | "$KUBECTL" apply -f - >/dev/null
apiVersion: v1
kind: PersistentVolume
metadata:
  name: pressure-records
spec:
  capacity:
    storage: 4Gi
  accessModes: ["ReadWriteOnce"]
  persistentVolumeReclaimPolicy: Delete
  storageClassName: pressure-records
  hostPath:
    path: /tmp/rm-monitor-pressure-records
    type: DirectoryOrCreate
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: pressure-records-shared
spec:
  capacity:
    storage: 4Gi
  accessModes: ["ReadWriteMany"]
  persistentVolumeReclaimPolicy: Delete
  storageClassName: pressure-records-shared
  hostPath:
    path: /tmp/rm-monitor-pressure-records
    type: DirectoryOrCreate
YAML

log "deploying dependencies"
"$KUBECTL" -n "$NS" create configmap e2e-mock-server --from-file=mock-server.py="$(host_path "$ROOT/scripts/e2e/mock-server.py")" >/dev/null
cat <<'YAML' | "$KUBECTL" -n "$NS" apply -f - >/dev/null
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
          image: python:3.12-alpine
          imagePullPolicy: IfNotPresent
          command: ["/bin/sh", "-lc"]
          args:
            - |
              set -eu
              apk add --no-cache ffmpeg
              mkdir -p /tmp/hls
              ffmpeg -hide_banner -loglevel error -re \
                -f lavfi -i testsrc2=size=320x180:rate=15 \
                -f lavfi -i anullsrc=channel_layout=mono:sample_rate=44100 \
                -c:v libx264 -preset ultrafast -pix_fmt yuv420p -g 30 \
                -c:a aac -f hls -hls_time 1 -hls_list_size 6 \
                -hls_flags delete_segments+append_list /tmp/hls/main.m3u8 &
              python -m http.server 8080 --directory /tmp/hls
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

"$KUBECTL" -n "$NS" rollout status deploy/postgres --timeout=120s >/dev/null
"$KUBECTL" -n "$NS" rollout status deploy/redis --timeout=120s >/dev/null
"$KUBECTL" -n "$NS" rollout status deploy/e2e-mock --timeout=120s >/dev/null
"$KUBECTL" -n "$NS" rollout status deploy/e2e-media --timeout=180s >/dev/null

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
  --set storage.record.storageClassName='pressure-records' \
  --set storage.record.volumeName='pressure-records' \
  --set storage.record.size='1Gi' \
  --set storage.shared.storageClassName='pressure-records-shared' \
  --set storage.shared.volumeName='pressure-records-shared' \
  --set storage.shared.size='1Gi' \
  --set components.larkNotifier.replicas=0 \
  --set components.recordDispatcher.replicas=0 \
  --set components.uploaderDispatcher.replicas=0 \
  --set components.transcodeDispatcher.replicas=0 \
  --set components.highlightDispatcher.replicas=0 >/dev/null

"$KUBECTL" -n "$NS" wait --for=condition=complete job/"$RELEASE-migrate" --timeout=240s >/dev/null
"$KUBECTL" -n "$NS" rollout status deploy/monitor --timeout=180s >/dev/null

psql() {
  "$KUBECTL" -n "$NS" exec deploy/postgres -- env PGPASSWORD=rm_monitor \
    psql -U rm_monitor -d rm_monitor "$@"
}

psql_file() {
  local file="$1"
  shift
  "$KUBECTL" -n "$NS" exec -i deploy/postgres -- env PGPASSWORD=rm_monitor \
    psql -U rm_monitor -d rm_monitor "$@" < "$file"
}

psql_value() {
  local sql="$1"
  psql -tAc "$sql" | tr -d '\r[:space:]'
}

mock_set() {
  local query="$1"
  "$KUBECTL" -n "$NS" run pressure-curl --rm -i --restart=Never --image=curlimages/curl:8.10.1 -- \
    -fsS "http://e2e-mock:8080/set?${query}" >/dev/null
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
  return 1
}

stat_tables() {
  psql -P pager=off -c "select relname, n_tup_ins, n_tup_upd, n_tup_del, n_dead_tup from pg_stat_user_tables where relname in ('matches','teams','match_rounds','record_tasks','media_artifacts','upload_tasks','transcode_tasks','ocr_tasks','highlight_clips','highlight_publish_tasks') order by relname;"
}

stat_indexes() {
  psql -P pager=off -c "select relname, indexrelname, idx_scan from pg_stat_user_indexes where relname in ('matches','match_rounds','record_tasks','media_artifacts','upload_tasks','transcode_tasks','ocr_tasks','highlight_clips','highlight_publish_tasks') and indexrelname ~ '(updated_at|kind_status)' order by relname, indexrelname;"
}

log "seeding pressure data profile=$PROFILE"
psql_file "$ROOT/scripts/e2e/db-pressure-seed.sql" -v "profile=$PROFILE"

log "simulating index migration over populated tables"
psql -v ON_ERROR_STOP=1 -c "
drop index if exists match_updated_at;
drop index if exists match_latest_status_updated_at;
drop index if exists matchround_updated_at;
drop index if exists matchround_status_updated_at;
drop index if exists recordtask_status_updated_at;
drop index if exists uploadtask_status_updated_at;
drop index if exists transcodetask_status_updated_at;
drop index if exists ocrtask_status_updated_at;
drop index if exists highlightclip_status_updated_at;
drop index if exists highlightpublishtask_status_updated_at;
drop index if exists mediaartifact_kind_status_created_at;
drop index if exists mediaartifact_kind_status_deletable_at;"
migration_start=$(date +%s)
"$HELM" upgrade --install "$RELEASE" "$(host_path "$ROOT/charts/rm-monitor")" \
  --namespace "$NS" \
  --set imagePullPolicy=IfNotPresent \
  --set jobImagePullPolicy=IfNotPresent \
  --set postgres.dsn='postgres://rm_monitor:rm_monitor@postgres:5432/rm_monitor?sslmode=disable' \
  --set redis.host='redis:6379' \
  --set monitor.scheduleURL='http://e2e-mock:8080/schedule.json' \
  --set record.liveInfoURL='http://e2e-mock:8080/live_game_info.json' \
  --set record.res='1080p' \
  --set storage.record.storageClassName='pressure-records' \
  --set storage.record.volumeName='pressure-records' \
  --set storage.record.size='1Gi' \
  --set storage.shared.storageClassName='pressure-records-shared' \
  --set storage.shared.volumeName='pressure-records-shared' \
  --set storage.shared.size='1Gi' \
  --set components.larkNotifier.replicas=0 \
  --set components.recordDispatcher.replicas=0 \
  --set components.uploaderDispatcher.replicas=0 \
  --set components.transcodeDispatcher.replicas=0 \
  --set components.highlightDispatcher.replicas=0 >/dev/null
"$KUBECTL" -n "$NS" wait --for=condition=complete job/"$RELEASE-migrate" --timeout=240s >/dev/null
migration_elapsed=$(( $(date +%s) - migration_start ))
log "migrate job index recreation elapsed=${migration_elapsed}s"

missing_indexes="$(psql_value "select count(*) from (values ('match_updated_at'),('match_latest_status_updated_at'),('matchround_updated_at'),('matchround_status_updated_at'),('recordtask_status_updated_at'),('uploadtask_status_updated_at'),('transcodetask_status_updated_at'),('ocrtask_status_updated_at'),('highlightclip_status_updated_at'),('highlightpublishtask_status_updated_at'),('mediaartifact_kind_status_created_at'),('mediaartifact_kind_status_deletable_at')) expected(name) where not exists (select 1 from pg_indexes where schemaname='public' and indexname=expected.name);")"
if [[ "$missing_indexes" != "0" ]]; then
  log "missing recreated indexes: $missing_indexes"
  exit 1
fi

log "starting record dispatcher after schema migration"
"$KUBECTL" -n "$NS" scale deploy/record-dispatcher --replicas=1 >/dev/null
"$KUBECTL" -n "$NS" rollout status deploy/record-dispatcher --timeout=180s >/dev/null

log "capturing table stats before idle"
stat_tables
before_updates="$(psql_value "select coalesce(sum(n_tup_upd),0) from pg_stat_user_tables where relname in ('matches','teams','record_tasks','upload_tasks','transcode_tasks');")"
before_match_max="$(psql_value "select extract(epoch from max(updated_at))::bigint from matches;")"

log "idle pressure window ${IDLE_SECONDS}s"
sleep "$IDLE_SECONDS"

after_updates="$(psql_value "select coalesce(sum(n_tup_upd),0) from pg_stat_user_tables where relname in ('matches','teams','record_tasks','upload_tasks','transcode_tasks');")"
after_match_max="$(psql_value "select extract(epoch from max(updated_at))::bigint from matches;")"
idle_update_delta=$(( after_updates - before_updates ))
log "idle update delta=$idle_update_delta match_max_updated_at_before=$before_match_max after=$after_match_max"

log "live started pressure window ${CHURN_SECONDS}s"
mock_set 'status=STARTED&red=0&blue=0&result=UNKNOWN'
wait_sql_ge "select count(*) from match_rounds where match_rounds='e2e-match-1' and status='STARTED'" 1 "e2e started rounds"
wait_sql_ge "select count(*) from record_tasks where status in ('DISPATCHING','RUNNING','SUCCEEDED') and output_path like 'E2E Event/%'" 1 "e2e record tasks running"

end=$((SECONDS + CHURN_SECONDS))
i=0
while (( SECONDS < end )); do
  mock_set "status=STARTED&red=$((i % 3))&blue=$(((i + 1) % 3))&result=UNKNOWN"
  i=$((i + 1))
  sleep 2
done
mock_set 'status=DONE&red=1&blue=0&result=RED'

wait_sql_ge "select count(*) from match_rounds where match_rounds='e2e-match-1' and status='ENDED'" 1 "e2e ended rounds"
wait_sql_ge "select count(*) from record_tasks where status='SUCCEEDED' and output_path like 'E2E Event/%'" 1 "e2e record tasks succeeded"
wait_sql_ge "select count(*) from media_artifacts where kind='source' and status='AVAILABLE' and path like 'E2E Event/%'" 1 "e2e source artifacts available"

log "final table stats"
stat_tables
log "new index usage"
stat_indexes
log "record dispatcher tick stats"
"$KUBECTL" -n "$NS" logs deploy/record-dispatcher --tail=80 | grep 'record dispatch tick source=' || true

log "pressure e2e passed profile=$PROFILE idle_update_delta=$idle_update_delta migrate_elapsed=${migration_elapsed}s"

param(
  [switch]$SkipBuild,
  [switch]$SkipArgoInstall,
  [switch]$PortForwardArgo,
  [switch]$EnableLiveDanmu,
  [switch]$EnableLarkUpload,
  [switch]$EnableSTTQuality
)

$ErrorActionPreference = "Stop"

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$local = Join-Path $repo ".local\k8s"
$manifests = Join-Path $local "manifests"
$records = Join-Path $local "records"
$shared = Join-Path $local "records-shared"
$namespace = "rm-monitor"
$context = "docker-desktop"
$mainRole = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String("5Li76KeG6KeS"))
$prodValues = "D:\src\kubernetes-deployments\rm-monitor\values-prod.yaml"
$localPrivateValues = "D:\src\rm-monitor\.local\helm-values-local.yaml"

$difyBaseURL = $env:RM_MONITOR_E2E_DIFY_BASE_URL
if (-not $difyBaseURL) {
  $difyBaseURL = "https://dify.scutbot.cn"
}
if ($difyBaseURL.TrimEnd("/") -match "/v1$") {
  throw "RM_MONITOR_E2E_DIFY_BASE_URL must not include /v1; use the Dify root URL, for example https://dify.scutbot.cn"
}
$manifestWorkflowAPIKey = $env:RM_MONITOR_E2E_MANIFEST_WORKFLOW_API_KEY
$sttQualityWorkflowAPIKey = $env:RM_MONITOR_E2E_STT_QUALITY_WORKFLOW_API_KEY
$whisperURL = $env:RM_MONITOR_E2E_WHISPER_URL
if (-not $whisperURL) {
  $whisperURL = "http://host.docker.internal:48088/inference"
}
$ocrURL = $env:RM_MONITOR_E2E_OCR_URL
if (-not $ocrURL) {
  $ocrURL = "http://host.docker.internal:48089"
}

function Read-YamlScalar($path, $parent, $key) {
  if (-not (Test-Path $path)) {
    return ""
  }
  $inParent = $false
  foreach ($line in Get-Content $path) {
    if ($line -match "^$([regex]::Escape($parent)):\s*$") {
      $inParent = $true
      continue
    }
    if ($inParent -and $line -match "^\S") {
      return ""
    }
    if ($inParent -and $line -match "^\s+$([regex]::Escape($key)):\s*(.+?)\s*$") {
      return $Matches[1].Trim().Trim('"').Trim("'")
    }
  }
  return ""
}

function Run($cmd) {
  Write-Host ">> $cmd"
  iex $cmd
  if ($LASTEXITCODE -ne 0) {
    throw "command failed with exit code $LASTEXITCODE"
  }
}

function HostReachableURL($url) {
  return $url -replace "://host\.docker\.internal(:|/|$)", "://127.0.0.1`$1"
}

function Test-HTTPReachable($name, $url) {
  $hostURL = HostReachableURL $url
  $nullOut = "NUL"
  & curl.exe -sS --max-time 5 -o $nullOut $hostURL
  if ($LASTEXITCODE -ne 0) {
    throw "$name is not reachable at $url (host check used $hostURL)"
  }
}

function DockerContainerStatus($containerName) {
  $status = docker ps -a --filter "name=$containerName" --format "{{.Names}} {{.Status}} {{.Ports}}" 2>$null
  if ($LASTEXITCODE -ne 0 -or -not $status) {
    return "$containerName container was not found"
  }
  return ($status -join "; ")
}

function HostPathForDockerDesktop($path) {
  $resolved = (Resolve-Path $path).Path -replace "\\", "/"
  if ($resolved -match "^([A-Za-z]):/(.*)$") {
    return "/run/desktop/mnt/host/$($Matches[1].ToLower())/$($Matches[2])"
  }
  return $resolved
}

New-Item -ItemType Directory -Force $manifests, $records, $shared | Out-Null

if (-not $manifestWorkflowAPIKey) {
  throw "RM_MONITOR_E2E_MANIFEST_WORKFLOW_API_KEY is required for real Dify manifest workflow"
}
if ($EnableSTTQuality -and -not $sttQualityWorkflowAPIKey) {
  throw "EnableSTTQuality requires RM_MONITOR_E2E_STT_QUALITY_WORKFLOW_API_KEY"
}
Test-HTTPReachable "Dify" $difyBaseURL
try {
  Test-HTTPReachable "Whisper server" $whisperURL
} catch {
  $status = DockerContainerStatus "whisper-server"
  throw "Whisper server is not reachable at $whisperURL. Docker status: $status. Start the local whisper.cpp server, for example from your whisper-server docker compose directory. Original error: $($_.Exception.Message)"
}
$ocrHealthURL = ($ocrURL.TrimEnd("/") + "/health")
Test-HTTPReachable "OCR server" $ocrHealthURL

$danmuEnabled = "false"
$danmuValues = ""
if ($EnableLiveDanmu) {
  $danmuAppId = $env:RM_MONITOR_E2E_DANMU_APP_ID
  $danmuAppKey = $env:RM_MONITOR_E2E_DANMU_APP_KEY
  if (-not $danmuAppId) {
    $danmuAppId = Read-YamlScalar $prodValues "danmu" "appId"
  }
  if (-not $danmuAppKey) {
    $danmuAppKey = Read-YamlScalar $prodValues "danmu" "appKey"
  }
  if (-not $danmuAppId -or -not $danmuAppKey) {
    throw "EnableLiveDanmu requires RM_MONITOR_E2E_DANMU_APP_ID/RM_MONITOR_E2E_DANMU_APP_KEY or $prodValues danmu.appId/appKey"
  }
  $danmuEnabled = "true"
  $danmuValues = @"
  appId: "$danmuAppId"
  appKey: "$danmuAppKey"
"@
}

$larkValues = ""
if ($EnableLarkUpload) {
  $larkAppId = $env:RM_MONITOR_E2E_LARK_APP_ID
  $larkAppSecret = $env:RM_MONITOR_E2E_LARK_APP_SECRET
  $bitableAppToken = $env:RM_MONITOR_E2E_BITABLE_APP_TOKEN
  if (-not $larkAppId) {
    $larkAppId = Read-YamlScalar $localPrivateValues "lark" "appId"
  }
  if (-not $larkAppSecret) {
    $larkAppSecret = Read-YamlScalar $localPrivateValues "lark" "appSecret"
  }
  if (-not $bitableAppToken) {
    $bitableAppToken = Read-YamlScalar $localPrivateValues "upload" "bitableAppToken"
  }
  if (-not $larkAppId -or -not $larkAppSecret -or -not $bitableAppToken) {
    throw "EnableLarkUpload requires RM_MONITOR_E2E_LARK_APP_ID/RM_MONITOR_E2E_LARK_APP_SECRET/RM_MONITOR_E2E_BITABLE_APP_TOKEN or $localPrivateValues"
  }
  $larkValues = @"
lark:
  appId: "$larkAppId"
  appSecret: "$larkAppSecret"
upload:
  bitableAppToken: "$bitableAppToken"
  concurrency: 1
  partRetries: 3
  retryBackoff: 2
  rateLimitPerMinute: 30
"@
}

if (-not $SkipArgoInstall) {
  Run "kubectl --context $context get ns argo 2>`$null || kubectl --context $context create ns argo"
  Run "helm repo add argo https://argoproj.github.io/argo-helm 2>`$null"
  Run "helm repo update argo"
  Run "helm --kube-context $context upgrade --install argo-workflows argo/argo-workflows -n argo --create-namespace --set server.enabled=true --set server.extraArgs='{--auth-mode=server}' --wait --timeout 10m"
}

if ($PortForwardArgo) {
  $log = Join-Path $local "argo-port-forward.log"
  $err = Join-Path $local "argo-port-forward.err.log"
  $existing = Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -like "*port-forward*svc/argo-workflows-server*2746:2746*" }
  if (-not $existing) {
    Start-Process kubectl -ArgumentList @("--context", $context, "-n", "argo", "port-forward", "svc/argo-workflows-server", "2746:2746", "--address", "127.0.0.1") -RedirectStandardOutput $log -RedirectStandardError $err -WindowStyle Hidden | Out-Null
  }
  Write-Host "Argo UI: http://127.0.0.1:2746"
}

$recordsHost = HostPathForDockerDesktop $records
$sharedHost = HostPathForDockerDesktop $shared
@"
apiVersion: v1
kind: PersistentVolume
metadata:
  name: records
spec:
  capacity:
    storage: 50Gi
  accessModes: [ReadWriteMany]
  persistentVolumeReclaimPolicy: Retain
  storageClassName: records
  hostPath:
    path: $recordsHost
    type: DirectoryOrCreate
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: records-shared
spec:
  capacity:
    storage: 50Gi
  accessModes: [ReadWriteMany]
  persistentVolumeReclaimPolicy: Retain
  storageClassName: records-shared
  hostPath:
    path: $sharedHost
    type: DirectoryOrCreate
"@ | Set-Content (Join-Path $manifests "local-pv.yaml") -Encoding utf8

@"
apiVersion: apps/v1
kind: Deployment
metadata:
  name: postgres
  namespace: rm-monitor
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
  namespace: rm-monitor
spec:
  selector: {app: postgres}
  ports:
    - {port: 5432, targetPort: 5432}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis
  namespace: rm-monitor
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
  namespace: rm-monitor
spec:
  selector: {app: redis}
  ports:
    - {port: 6379, targetPort: 6379}
"@ | Set-Content (Join-Path $manifests "deps.yaml") -Encoding utf8

@"
global:
  imagePullPolicy: IfNotPresent
  jobImagePullPolicy: IfNotPresent
imagePullPolicy: IfNotPresent
jobImagePullPolicy: IfNotPresent
$larkValues
storage:
  record:
    pvcName: records
    storageClassName: records
    size: 50Gi
    volumeName: records
  shared:
    pvcName: records-shared
    storageClassName: records-shared
    size: 50Gi
    volumeName: records-shared
components:
  matchController:
    replicas: 1
  larkNotifier:
    replicas: 0
  healthChecker:
    schedule: "0 0 1 1 *"
    successfulJobsHistoryLimit: 1
    failedJobsHistoryLimit: 1
    resources:
      requests:
        cpu: 20m
        memory: 64Mi
      limits:
        cpu: 200m
        memory: 256Mi
  artifactCleaner:
    schedule: "0 0 1 1 *"
    successfulJobsHistoryLimit: 1
    failedJobsHistoryLimit: 1
    resources:
      requests:
        cpu: 20m
        memory: 64Mi
      limits:
        cpu: 200m
        memory: 256Mi
controller:
  scheduleURL: http://e2e-mock:8080/schedule.json
record:
  liveInfoURL: http://e2e-mock:8080/live_game_info.json
  res: 1080p
  stopDelaySeconds: 5
stt:
  enabled: true
  role: "$mainRole"
  whisperServerUrls:
    - $whisperURL
  useQuality: $($EnableSTTQuality.ToString().ToLowerInvariant())
  qualityWorkflowAPIKey: "$sttQualityWorkflowAPIKey"
analyze:
  enabled: true
  role: "$mainRole"
  startScanStepSeconds: 2
  settlementScanStepSeconds: 2
  maxStartScanSeconds: 60
  maxSettlementScanSeconds: 60
dify:
  baseURL: $difyBaseURL
manifest:
  reportWorkflowAPIKey: "$manifestWorkflowAPIKey"
ocrServer:
  baseURL: $ocrURL
  timeoutSeconds: 30
transcode:
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: 1
      memory: 512Mi
danmu:
  enabled: $danmuEnabled
$danmuValues
highlight:
  enabled: false
"@ | Set-Content (Join-Path $local "values.local.yaml") -Encoding utf8

@"
apiVersion: v1
kind: ConfigMap
metadata:
  name: e2e-mock-server
  namespace: rm-monitor
data:
  mock-server.py: |
    PLACEHOLDER
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: e2e-mock
  namespace: rm-monitor
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
          configMap: {name: e2e-mock-server}
---
apiVersion: v1
kind: Service
metadata:
  name: e2e-mock
  namespace: rm-monitor
spec:
  selector: {app: e2e-mock}
  ports:
    - {port: 8080, targetPort: 8080}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: e2e-media
  namespace: rm-monitor
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
          command: ["/bin/sh", "-lc"]
          args:
            - |
              set -eu
              apk add --no-cache ffmpeg
              mkdir -p /tmp/hls
              ffmpeg -hide_banner -loglevel error -f lavfi -i testsrc2=size=320x180:rate=15 -f lavfi -i sine=frequency=880:sample_rate=44100 -t 20 -c:v libx264 -preset ultrafast -pix_fmt yuv420p -g 30 -c:a aac -y /tmp/pattern.mp4
              ffmpeg -hide_banner -loglevel error -re -stream_loop -1 -i /tmp/pattern.mp4 -c copy -f hls -hls_time 2 -hls_list_size 8 -hls_flags delete_segments+append_list /tmp/hls/main.m3u8 &
              cd /tmp/hls
              python -m http.server 8080
          ports:
            - {containerPort: 8080}
---
apiVersion: v1
kind: Service
metadata:
  name: e2e-media
  namespace: rm-monitor
spec:
  selector: {app: e2e-media}
  ports:
    - {port: 8080, targetPort: 8080}
"@ | Set-Content (Join-Path $manifests "e2e-mock-media.yaml") -Encoding utf8

if (-not $SkipBuild) {
  $images = @(
    @("migrate-job", "migrate-job/Dockerfile"),
    @("match-controller", "match-controller/Dockerfile"),
    @("record-job", "record-job/Dockerfile"),
    @("stt-job", "stt-job/Dockerfile"),
    @("transcode-job", "transcode-job/Dockerfile"),
    @("manifest-job", "manifest-job/Dockerfile"),
    @("analyze-job", "analyze-job/Dockerfile")
  )
  foreach ($image in $images) {
    Run "docker build -t ghcr.io/scutrobotlab/rm-monitor/$($image[0]):dev -f $($image[1]) ."
  }
}

Run "kubectl --context $context create ns $namespace --dry-run=client -o yaml | kubectl --context $context apply -f -"
Run "kubectl --context $context -n $namespace run e2e-preflight-whisper --rm -i --restart=Never --image=curlimages/curl:8.10.1 --command -- curl -sS --max-time 5 -o /dev/null $whisperURL"
Run "kubectl --context $context -n $namespace run e2e-preflight-ocr --rm -i --restart=Never --image=curlimages/curl:8.10.1 --command -- curl -fsS --max-time 5 $($ocrURL.TrimEnd('/'))/health"
Run "kubectl --context $context apply -f `"$manifests\local-pv.yaml`""
Run "kubectl --context $context apply -f `"$manifests\deps.yaml`""
Run "kubectl --context $context -n $namespace rollout status deploy/postgres --timeout=120s"
Run "kubectl --context $context -n $namespace rollout status deploy/redis --timeout=120s"

$mock = Get-Content (Join-Path $repo "scripts\e2e\mock-server.py") -Raw
$mockIndented = ($mock -split "`r?`n" | ForEach-Object { "    " + $_ }) -join "`n"
$mockTemplate = Get-Content (Join-Path $manifests "e2e-mock-media.yaml") -Raw
($mockTemplate -replace "    PLACEHOLDER", $mockIndented) | Set-Content (Join-Path $manifests "e2e-mock-media.rendered.yaml") -Encoding utf8 -NoNewline
Run "kubectl --context $context apply -f `"$manifests\e2e-mock-media.rendered.yaml`""
Run "kubectl --context $context -n $namespace rollout restart deploy/e2e-mock"
Run "kubectl --context $context -n $namespace rollout status deploy/e2e-mock --timeout=120s"
Run "kubectl --context $context -n $namespace rollout status deploy/e2e-media --timeout=120s"

$values = Join-Path $local "values.local.yaml"
Run "helm --kube-context $context upgrade --install rm-monitor ./charts/rm-monitor -n $namespace --create-namespace -f `"$values`" --wait --timeout 5m"
Run "kubectl --context $context -n $namespace rollout restart deploy/match-controller"
Run "kubectl --context $context -n $namespace rollout status deploy/match-controller --timeout=120s"

$workflowName = "match-1-e2e-match-1"
Run "kubectl --context $context -n $namespace delete wf $workflowName match-e2e-event-e2e-zone-1-e2e-match-1 rm-match-e2e-match-1 --ignore-not-found"
$cleanupDeadline = (Get-Date).AddMinutes(2)
do {
  $leftoverPods = kubectl --context $context -n $namespace get pods -l workflows.argoproj.io/workflow=$workflowName --ignore-not-found 2>$null
  if (-not ($leftoverPods | Select-String $workflowName)) {
    break
  }
  Start-Sleep -Seconds 2
} while ((Get-Date) -lt $cleanupDeadline)
if ($leftoverPods | Select-String $workflowName) {
  throw "workflow $workflowName pods did not terminate before local cleanup"
}
Remove-Item -Recurse -Force (Join-Path $records "*") -ErrorAction SilentlyContinue
Remove-Item -Recurse -Force (Join-Path $shared "*") -ErrorAction SilentlyContinue
Run "kubectl --context $context -n $namespace exec deploy/postgres -- psql -U rm_monitor -d rm_monitor -c `"truncate lark_bitable_records, lark_messages, bilibili_highlight_publications, highlight_clips, match_rounds, matches, teams cascade;`""
Run "kubectl --context $context -n $namespace exec deploy/e2e-mock -- wget -qO- 'http://127.0.0.1:8080/set?status=WAITING&red=0&blue=0&result=UNKNOWN'"
Start-Sleep -Seconds 3
Run "kubectl --context $context -n $namespace exec deploy/e2e-mock -- wget -qO- 'http://127.0.0.1:8080/set?status=STARTED&red=0&blue=0&result=UNKNOWN'"

$deadline = (Get-Date).AddSeconds(60)
do {
  $wf = kubectl --context $context -n $namespace get wf $workflowName --ignore-not-found 2>$null
  if ($wf) { Write-Host $wf; break }
  Start-Sleep -Seconds 2
} while ((Get-Date) -lt $deadline)
if (-not $wf) {
  throw "workflow $workflowName was not created"
}

function Wait-RoundRecordRunning($roundNo) {
  $recordDeadline = (Get-Date).AddMinutes(2)
  do {
    $json = kubectl --context $context -n $namespace get wf $workflowName -o json 2>$null
    if ($LASTEXITCODE -eq 0 -and $json) {
      $wfObj = $json | ConvertFrom-Json
      foreach ($prop in $wfObj.status.nodes.PSObject.Properties) {
        $node = $prop.Value
        if ($node.templateName -eq "record" -and $node.phase -eq "Running" -and $node.displayName -like "round-$roundNo-record*") {
          return
        }
      }
    }
    Start-Sleep -Seconds 2
  } while ((Get-Date) -lt $recordDeadline)
  throw "round $roundNo record node did not start"
}

Wait-RoundRecordRunning 1
Start-Sleep -Seconds 10
Run "kubectl --context $context -n $namespace exec deploy/e2e-mock -- wget -qO- 'http://127.0.0.1:8080/set?status=STARTED&red=1&blue=0&result=UNKNOWN'"

Wait-RoundRecordRunning 2
Start-Sleep -Seconds 10
Run "kubectl --context $context -n $namespace exec deploy/e2e-mock -- wget -qO- 'http://127.0.0.1:8080/set?status=STARTED&red=1&blue=1&result=UNKNOWN'"

Wait-RoundRecordRunning 3
Start-Sleep -Seconds 10
Run "kubectl --context $context -n $namespace exec deploy/e2e-mock -- wget -qO- 'http://127.0.0.1:8080/set?status=DONE&red=2&blue=1&result=RED'"

$deadline = (Get-Date).AddMinutes(5)
do {
  $phase = kubectl --context $context -n $namespace get wf $workflowName -o jsonpath='{.status.phase}' 2>$null
  if ($phase -in @("Succeeded", "Failed", "Error")) { break }
  Start-Sleep -Seconds 5
} while ((Get-Date) -lt $deadline)

Run "kubectl --context $context -n $namespace get wf $workflowName"
if ($phase -ne "Succeeded") {
  $wf = kubectl --context $context -n $namespace get wf $workflowName -o json | ConvertFrom-Json
  foreach ($nodeProp in $wf.status.nodes.PSObject.Properties) {
    $node = $nodeProp.Value
    if ($node.phase -in @("Failed", "Error", "Omitted")) {
      Write-Host "$($node.displayName)`t$($node.phase)`t$($node.message)"
    }
  }
  throw "workflow phase is $phase"
}

Run "kubectl --context $context -n $namespace exec deploy/postgres -- psql -U rm_monitor -d rm_monitor -c `"select id, latest_status, result, report is not null as has_report from matches; select id, round_no, status, winner from match_rounds order by id; select r.round_no, b.role, b.record_id is not null as has_record, b.attachment_file_token is not null as has_attachment from lark_bitable_records b join match_rounds r on r.id = b.match_round_lark_bitable_records order by r.round_no, b.role;`""
Write-Host "Local Argo E2E succeeded."
Write-Host "Records: $records"
Write-Host "Shared records: $shared"


# stt-recover

`stt-recover` is an operator tool for recovering STT output after a whisper-server outage.

It expects the same config shape as `stt-job` and should run where the records
volume is mounted. It writes `Round-N/stt.jsonl`, marks Redis STT status as
`DONE`, clears `matches.report`, and then lets `record-dispatcher` schedule the
normal `manifest-job`. The manifest job updates `README.md` and notifies
`lark-notifier` through `match_changed`, so this tool does not call Feishu APIs.

Examples:

```bash
stt-recover -f /etc/rm-monitor/config.yml -list -limit 20
stt-recover -f /etc/rm-monitor/config.yml -list -failed-only -limit 100
stt-recover -f /etc/rm-monitor/config.yml -round 293 -force
stt-recover -f /etc/rm-monitor/config.yml -match 31003
stt-recover -f /etc/rm-monitor/config.yml -failed-only -limit 100 -concurrency 2
stt-recover -f /etc/rm-monitor/config.yml -benchmark-source "RMUC 2026超级对抗赛/东部赛区/5. 中国科学技术大学-RoboWalker VS 青岛大学-未来/Round-1/主视角.flv"
```

Use `-no-report-reset` when only rebuilding `stt.jsonl`.
Use `-no-highlight-reset` when preserving failed/pending highlight records.

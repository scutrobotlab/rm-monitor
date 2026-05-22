# recording-repair

`recording-repair` repairs recording-derived artifacts after STT/highlight job boundary failures.

It is intentionally scoped and defaults to dry-run. Pass `-apply` to mutate files or PG state.

Main actions:

- generate missing round subtitle files from existing `stt.jsonl`
- remove finished `audio/` segment directories after STT has succeeded
- reset failed highlight clips caused by the cover-generation bug

Examples:

```bash
recording-repair -f /app/etc/config.yml -event "RMUC 2026超级对抗赛" -zone "东部赛区"
recording-repair -f /app/etc/config.yml -event "RMUC 2026超级对抗赛" -zone "东部赛区" -apply
recording-repair -f /app/etc/config.yml -round 379,380 -apply
```

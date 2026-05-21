# stt-subtitle

`stt-subtitle` scans the recordings directory and backfills subtitle files from existing `stt.jsonl`.

It does not call Whisper, mutate PG, or rerun STT. By default it only creates missing files:

- Round subtitle: `Round-N/<STTRole>.srt`
- Highlight subtitle: `Round-N/highlights/*/video.srt`

## Usage

```bash
stt-subtitle -f /app/etc/config.yml
```

List candidates without writing files:

```bash
stt-subtitle -f /app/etc/config.yml -list
```

Overwrite existing subtitles:

```bash
stt-subtitle -f /app/etc/config.yml -force
```

Only process round subtitles:

```bash
stt-subtitle -f /app/etc/config.yml -highlights=false
```

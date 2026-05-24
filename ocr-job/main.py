import json
import os
import subprocess
import sys
import time
from tempfile import TemporaryDirectory
from pathlib import Path

import cv2

from ocr_engine import read_settlement


ENV_NAME = "RM_MONITOR_JOB_CONTEXT"
JOB_DIR_NAME = ".job"
TAIL_SCAN_SECONDS = 600


def atomic_write_json(path: Path, data: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + ".tmp")
    tmp.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    tmp.replace(path)


def write_error(job_dir: Path, task_id: int, message: str) -> None:
    atomic_write_json(
        job_dir / "error.json",
        {
            "schema": "rm-monitor/job-error/v1",
            "task_type": "ocr",
            "task_id": task_id,
            "status": "FAILED",
            "error_message": message[-4096:],
            "completed_at": iso_now(),
        },
    )


def iso_now() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def rel_or_abs(base_dir: str, source_path: str) -> Path:
    path = Path(source_path)
    if path.is_absolute():
        return path
    return Path(base_dir) / source_path


def job_dir(ctx: dict) -> Path:
    return Path(ctx["round_dir"]) / JOB_DIR_NAME / f"ocr-{ctx['task_id']}"


def probe_duration(source: Path) -> float:
    proc = subprocess.run(
        [
            "ffprobe",
            "-v",
            "error",
            "-show_entries",
            "format=duration",
            "-of",
            "default=noprint_wrappers=1:nokey=1",
            str(source),
        ],
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=30,
        check=False,
    )
    try:
        return max(float(proc.stdout.strip()), 0.0)
    except ValueError:
        return 0.0


def extract_frames(source: Path, frame_dir: Path, frame_interval: int) -> float:
    duration = probe_duration(source)
    start_seconds = max(duration - TAIL_SCAN_SECONDS, 0.0) if duration > 0 else 0.0
    fps = 1.0 / float(frame_interval)
    cmd = [
        "ffmpeg",
        "-hide_banner",
        "-loglevel",
        "warning",
        "-nostdin",
    ]
    if start_seconds > 0:
        cmd += ["-ss", f"{start_seconds:.3f}"]
    cmd += [
        "-i",
        str(source),
        "-vf",
        f"fps={fps:.6f}",
        "-q:v",
        "3",
        str(frame_dir / "frame-%08d.jpg"),
    ]
    print(
        f"extract source={source} duration={duration:.1f}s start={start_seconds:.1f}s interval={frame_interval}s",
        flush=True,
    )
    proc = subprocess.run(cmd, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    if proc.returncode != 0:
        raise RuntimeError(f"ffmpeg frame extraction failed: {proc.stderr[-2048:]}")
    return start_seconds


def scan_video(ctx: dict, job_dir_path: Path) -> dict:
    source = rel_or_abs(ctx.get("base_dir", "/records"), ctx["source_path"])
    round_dir = Path(ctx["round_dir"])
    frame_interval = max(int(ctx.get("frame_interval") or 1), 1)
    settlement_path = round_dir / "settlement.jpg"
    settlement_json = round_dir / "settlement.json"

    with TemporaryDirectory(prefix="rm-monitor-ocr-") as tmp:
        frame_dir = Path(tmp)
        start_seconds = extract_frames(source, frame_dir, frame_interval)
        frames = sorted(frame_dir.glob("frame-*.jpg"))
        print(f"scan extracted_frames={len(frames)}", flush=True)
        indexed_frames = list(enumerate(frames))
        for idx, frame_path in reversed(indexed_frames):
            frame = cv2.imread(str(frame_path))
            if frame is None:
                continue
            scanned = len(frames) - idx
            if scanned % 30 == 0:
                print(f"scanned_frames={scanned} current_second={start_seconds + idx * frame_interval:.1f}", flush=True)
            matched, fields, report = read_settlement(frame)
            if matched:
                round_dir.mkdir(parents=True, exist_ok=True)
                if not cv2.imwrite(str(settlement_path), frame):
                    raise RuntimeError(f"write settlement image failed: {settlement_path}")
                detected_at = start_seconds + idx * frame_interval
                data = {
                    "schema": "rm-monitor/settlement-ocr/v1",
                    "task_id": ctx["task_id"],
                    "match_round_id": ctx["match_round_id"],
                    "source_artifact_id": ctx["source_artifact_id"],
                    "match_id": ctx.get("match_id", ""),
                    "round_no": ctx.get("round_no", 0),
                    "role": ctx.get("role", ""),
                    "source_path": ctx["source_path"],
                    "detected_at_seconds": detected_at,
                    "fields": fields,
                    "report_text": report,
                }
                atomic_write_json(settlement_json, data)
                return {
                    "schema": "rm-monitor/ocr-result/v1",
                    "task_id": ctx["task_id"],
                    "match_round_id": ctx["match_round_id"],
                    "source_artifact_id": ctx["source_artifact_id"],
                    "settlement_path": str(settlement_path),
                    "settlement_json": str(settlement_json),
                    "completed_at": iso_now(),
                }

    raise RuntimeError("no valid settlement frame found")


def main() -> int:
    raw = os.environ.get(ENV_NAME, "")
    if not raw:
        print(f"{ENV_NAME} is required", file=sys.stderr)
        return 1
    try:
        ctx = json.loads(raw)
        job_dir_path = job_dir(ctx)
        atomic_write_json(job_dir_path / "context.json", ctx)
        result = scan_video(ctx, job_dir_path)
        atomic_write_json(job_dir_path / "result.json", result)
        return 0
    except Exception as exc:
        try:
            ctx = json.loads(raw)
            write_error(job_dir(ctx), int(ctx.get("task_id") or 0), str(exc))
        except Exception:
            pass
        print(str(exc), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

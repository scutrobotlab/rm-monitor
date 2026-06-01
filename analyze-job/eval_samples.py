#!/usr/bin/env python3
"""Run analyze-job against local sample FLVs.

The sample data is intentionally git-ignored. This script is only for local
evaluation and emits a compact summary for manual audit.
"""

from __future__ import annotations

import argparse
import csv
import json
import subprocess
import time
from pathlib import Path


def run_sample(source: Path, output_dir: Path, ocr_server_url: str) -> dict:
    job_name = f"analyze-{source.stem}"
    cmd = [
        "python",
        str(Path(__file__).parent / "main.py"),
        "--source",
        str(source),
        "--output-dir",
        str(output_dir),
        "--job-name",
        job_name,
        "--role",
        "主视角",
        "--ocr-server-url",
        ocr_server_url,
    ]
    started = time.perf_counter()
    proc = subprocess.run(cmd, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    elapsed = time.perf_counter() - started
    if proc.returncode != 0:
        return {
            "sample": source.name,
            "status": "FAILED",
            "elapsed_seconds": elapsed,
            "error": proc.stderr[-2000:],
        }
    result = json.loads(proc.stdout.strip().splitlines()[-1])
    round_json_path = output_dir / "round.json"
    round_json = json.loads(round_json_path.read_text(encoding="utf-8"))
    settlement = round_json.get("settlement", {}) or {}
    settlement_ocr = settlement.get("ocr") or {}
    settlement_data = {}
    if settlement_ocr:
        settlement_data = settlement_ocr.get("data", {})
    return {
        "sample": source.name,
        "status": round_json.get("analysis", {}).get("status", "UNKNOWN"),
        "elapsed_seconds": elapsed,
        "duration_seconds": round_json.get("duration_seconds"),
        "start_seconds": round_json.get("boundary", {}).get("start_seconds"),
        "end_seconds": round_json.get("boundary", {}).get("end_seconds"),
        "settlement_status": settlement.get("status"),
        "settlement_path": result.get("settlement_image_path", ""),
        "settlement_ocr_path": "",
        "red_score_reference": settlement_data.get("red", {}).get("score_reference"),
        "blue_score_reference": settlement_data.get("blue", {}).get("score_reference"),
        "victory_condition": settlement_data.get("victory_condition", ""),
        "errors": "; ".join(round_json.get("analysis", {}).get("errors", [])),
    }


def write_summary(rows: list[dict], out_dir: Path) -> None:
    csv_path = out_dir / "summary.csv"
    md_path = out_dir / "summary.md"
    fields = [
        "sample",
        "status",
        "elapsed_seconds",
        "duration_seconds",
        "start_seconds",
        "end_seconds",
        "settlement_status",
        "red_score_reference",
        "blue_score_reference",
        "victory_condition",
        "errors",
    ]
    with csv_path.open("w", encoding="utf-8", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fields, extrasaction="ignore")
        writer.writeheader()
        writer.writerows(rows)
    lines = [
        "| sample | status | elapsed | start | end | settlement | score_ref | victory | errors |",
        "|---|---:|---:|---:|---:|---|---|---|---|",
    ]
    for row in rows:
        lines.append(
            "| {sample} | {status} | {elapsed:.1f}s | {start} | {end} | {settlement} | {red}:{blue} | {victory} | {errors} |".format(
                sample=row.get("sample", ""),
                status=row.get("status", ""),
                elapsed=float(row.get("elapsed_seconds") or 0),
                start=format_seconds(row.get("start_seconds")),
                end=format_seconds(row.get("end_seconds")),
                settlement=row.get("settlement_status", ""),
                red=row.get("red_score_reference", ""),
                blue=row.get("blue_score_reference", ""),
                victory=(row.get("victory_condition") or "").replace("|", "/"),
                errors=(row.get("errors") or "").replace("|", "/"),
            )
        )
    md_path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def format_seconds(value) -> str:
    if value is None or value == "":
        return ""
    return f"{float(value):.1f}"


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--data-dir", type=Path, default=Path(__file__).parent / "data")
    parser.add_argument("--out-dir", type=Path, default=Path(__file__).parent / "debug" / "eval")
    parser.add_argument("--ocr-server-url", default="http://127.0.0.1:48089")
    parser.add_argument("--limit", type=int, default=0)
    args = parser.parse_args()

    sources = sorted(args.data_dir.glob("sample*.flv"))
    if args.limit > 0:
        sources = sources[: args.limit]
    args.out_dir.mkdir(parents=True, exist_ok=True)
    rows = []
    for source in sources:
        sample_out = args.out_dir / source.stem
        print(f"analyze {source.name}", flush=True)
        rows.append(run_sample(source, sample_out, args.ocr_server_url))
    write_summary(rows, args.out_dir)
    print(json.dumps({"out_dir": str(args.out_dir), "samples": len(rows)}, ensure_ascii=False))


if __name__ == "__main__":
    main()

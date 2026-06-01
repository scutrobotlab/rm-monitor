#!/usr/bin/env python3
"""Production-oriented round analyzer.

The analyzer streams low-resolution frames from ffmpeg rawvideo pipes and scores
layouts with OpenCV. It does not materialize intermediate frame files. Start and
settlement scans run independently and in parallel.
"""

from __future__ import annotations

import argparse
import io
import json
import mimetypes
import queue
import re
import subprocess
import threading
import time
import urllib.parse
import urllib.request
import uuid
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable, Iterator

import cv2
import numpy as np


PTS_RE = re.compile(r"pts_time:([-0-9.]+)")

DEFAULT_ROI_PATH = Path(__file__).parent / "settlement_roi.json"


def atomic_write_json(path: Path, payload: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + ".tmp")
    tmp.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    tmp.replace(path)


def ffprobe_duration(path: Path) -> float:
    proc = subprocess.run(
        [
            "ffprobe",
            "-v",
            "error",
            "-show_entries",
            "format=duration",
            "-of",
            "default=noprint_wrappers=1:nokey=1",
            str(path),
        ],
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr[-2000:])
    return float(proc.stdout.strip())


def crop(img: np.ndarray, x1: float, y1: float, x2: float, y2: float) -> np.ndarray:
    h, w = img.shape[:2]
    return img[int(y1 * h) : int(y2 * h), int(x1 * w) : int(x2 * w)]


def hsv_ratio(img: np.ndarray, lower: tuple[int, int, int], upper: tuple[int, int, int]) -> float:
    if img.size == 0:
        return 0.0
    hsv = cv2.cvtColor(img, cv2.COLOR_BGR2HSV)
    mask = cv2.inRange(hsv, np.array(lower, np.uint8), np.array(upper, np.uint8))
    return float(cv2.countNonZero(mask)) / float(mask.shape[0] * mask.shape[1])


def red_ratio(img: np.ndarray) -> float:
    if img.size == 0:
        return 0.0
    hsv = cv2.cvtColor(img, cv2.COLOR_BGR2HSV)
    r1 = cv2.inRange(hsv, np.array((0, 45, 45), np.uint8), np.array((12, 255, 255), np.uint8))
    r2 = cv2.inRange(hsv, np.array((165, 45, 45), np.uint8), np.array((180, 255, 255), np.uint8))
    mask = cv2.bitwise_or(r1, r2)
    return float(cv2.countNonZero(mask)) / float(mask.shape[0] * mask.shape[1])


def blue_ratio(img: np.ndarray) -> float:
    return hsv_ratio(img, (95, 45, 45), (135, 255, 255))


def cyan_ratio(img: np.ndarray) -> float:
    return hsv_ratio(img, (78, 35, 80), (105, 255, 255))


def white_edge_ratio(img: np.ndarray) -> float:
    if img.size == 0:
        return 0.0
    gray = cv2.cvtColor(img, cv2.COLOR_BGR2GRAY)
    bright = cv2.inRange(gray, 180, 255)
    edges = cv2.Canny(gray, 80, 180)
    return float(cv2.countNonZero(cv2.bitwise_or(bright, edges))) / float(gray.shape[0] * gray.shape[1])


def score_pregame(img: np.ndarray) -> tuple[float, dict[str, float]]:
    left_top = crop(img, 0.02, 0.00, 0.39, 0.10)
    right_top = crop(img, 0.61, 0.00, 0.98, 0.10)
    center_top = crop(img, 0.40, 0.00, 0.60, 0.12)
    bottom_center = crop(img, 0.35, 0.72, 0.65, 0.92)
    battle_top = crop(img, 0.00, 0.00, 1.00, 0.16)

    left_red = red_ratio(left_top)
    right_blue = blue_ratio(right_top)
    center_edges = white_edge_ratio(center_top)
    bottom_cyan = cyan_ratio(bottom_center)
    bottom_blue = blue_ratio(bottom_center)
    top_edges = white_edge_ratio(battle_top)

    score = (
        min(left_red / 0.55, 1.0) * 0.35
        + min(right_blue / 0.55, 1.0) * 0.35
        + max(0.0, 1.0 - center_edges / 0.22) * 0.10
        + min(bottom_cyan / 0.05, 1.0) * 0.10
        + max(0.0, 1.0 - top_edges / 0.18) * 0.10
    )
    if left_red < 0.50 or right_blue < 0.50 or bottom_blue > 0.40 or top_edges > 0.18:
        score *= 0.35
    return score, {
        "left_red": left_red,
        "right_blue": right_blue,
        "center_edges": center_edges,
        "bottom_cyan": bottom_cyan,
        "bottom_blue": bottom_blue,
        "top_edges": top_edges,
    }


def score_settlement(img: np.ndarray) -> tuple[float, dict[str, float]]:
    left_half = crop(img, 0.00, 0.00, 0.47, 0.75)
    right_half = crop(img, 0.53, 0.00, 1.00, 0.75)
    center = crop(img, 0.38, 0.04, 0.62, 0.28)
    lower_red = crop(img, 0.18, 0.48, 0.50, 0.78)
    lower_blue = crop(img, 0.50, 0.48, 0.82, 0.78)
    card_left = crop(img, 0.10, 0.26, 0.38, 0.42)
    card_right = crop(img, 0.62, 0.26, 0.90, 0.42)
    bottom_strip = crop(img, 0.00, 0.86, 1.00, 1.00)
    mid = crop(img, 0.00, 0.18, 1.00, 0.84)

    left_red = red_ratio(left_half)
    right_blue = blue_ratio(right_half)
    center_edges = white_edge_ratio(center)
    lower_red_ratio = red_ratio(lower_red)
    lower_blue_ratio = blue_ratio(lower_blue)
    card_red = red_ratio(card_left)
    card_blue = blue_ratio(card_right)
    bottom_v = float(cv2.cvtColor(bottom_strip, cv2.COLOR_BGR2HSV)[:, :, 2].mean()) / 255.0
    bottom_edges = white_edge_ratio(bottom_strip)
    mid_edges = white_edge_ratio(mid)

    score = (
        min(left_red / 0.10, 1.0) * 0.20
        + min(right_blue / 0.10, 1.0) * 0.20
        + min(center_edges / 0.14, 1.0) * 0.12
        + min(lower_red_ratio / 0.08, 1.0) * 0.15
        + min(lower_blue_ratio / 0.08, 1.0) * 0.15
        + min((card_red + card_blue) / 0.16, 1.0) * 0.10
        + max(0.0, 1.0 - bottom_v / 0.22) * 0.05
        + max(0.0, 1.0 - bottom_edges / 0.08) * 0.03
    )
    if bottom_v > 0.30 or bottom_edges > 0.10 or mid_edges > 0.13:
        score *= 0.55
    if left_red < 0.30 or right_blue < 0.45 or (card_red + card_blue) < 0.80:
        score *= 0.35
    return score, {
        "left_red": left_red,
        "right_blue": right_blue,
        "center_edges": center_edges,
        "lower_red": lower_red_ratio,
        "lower_blue": lower_blue_ratio,
        "card_red": card_red,
        "card_blue": card_blue,
        "bottom_v": bottom_v,
        "bottom_edges": bottom_edges,
        "mid_edges": mid_edges,
    }


def ffprobe_dimensions(path: Path) -> tuple[int, int]:
    proc = subprocess.run(
        [
            "ffprobe",
            "-v",
            "error",
            "-select_streams",
            "v:0",
            "-show_entries",
            "stream=width,height",
            "-of",
            "json",
            str(path),
        ],
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr[-2000:])
    streams = json.loads(proc.stdout).get("streams") or []
    if not streams:
        raise RuntimeError(f"no video stream found: {path}")
    return int(streams[0]["width"]), int(streams[0]["height"])


@dataclass
class Detection:
    second: float
    score: float
    details: dict[str, float]
    frame: np.ndarray | None = field(default=None, repr=False)


@dataclass
class Segment:
    status: str
    start_seconds: float
    end_seconds: float
    best_seconds: float
    avg_score: float
    max_score: float
    matched_frames: int
    details: dict[str, float] = field(default_factory=dict)
    best_frame: np.ndarray | None = field(default=None, repr=False)


@dataclass
class ScannerConfig:
    name: str
    threshold: float
    min_frames: int
    min_duration_seconds: float
    max_gap_seconds: float


PREGAME_CONFIG = ScannerConfig(
    name="pregame",
    threshold=0.62,
    min_frames=3,
    min_duration_seconds=2.0,
    max_gap_seconds=2.0,
)

SETTLEMENT_CONFIG = ScannerConfig(
    name="settlement",
    threshold=0.60,
    min_frames=3,
    min_duration_seconds=2.0,
    max_gap_seconds=2.0,
)


class StableSegmentTracker:
    def __init__(self, conf: ScannerConfig):
        self.conf = conf
        self.active: list[Detection] = []
        self.last_match_second: float | None = None
        self.accepted: list[Segment] = []

    def feed(self, det: Detection) -> None:
        if det.score >= self.conf.threshold:
            if self.active and self.last_match_second is not None and det.second - self.last_match_second > self.conf.max_gap_seconds:
                self._flush()
            self.active.append(det)
            self.last_match_second = det.second
            return
        if self.active and self.last_match_second is not None and det.second - self.last_match_second > self.conf.max_gap_seconds:
            self._flush()

    def finish(self) -> list[Segment]:
        self._flush()
        return self.accepted

    def _flush(self) -> None:
        if not self.active:
            return
        first = self.active[0]
        last = self.active[-1]
        duration = last.second - first.second
        if len(self.active) >= self.conf.min_frames and duration >= self.conf.min_duration_seconds:
            best = max(self.active, key=lambda d: d.score)
            avg = sum(d.score for d in self.active) / len(self.active)
            self.accepted.append(
                Segment(
                    status="CONFIRMED",
                    start_seconds=first.second,
                    end_seconds=last.second,
                    best_seconds=best.second,
                    avg_score=avg,
                    max_score=best.score,
                    matched_frames=len(self.active),
                    details=best.details,
                    best_frame=best.frame.copy() if best.frame is not None else None,
                )
            )
        self.active = []
        self.last_match_second = None


def run(cmd: list[str]) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)


def collect_pts(stderr, pts_queue: queue.Queue[float]) -> None:
    for raw in iter(stderr.readline, b""):
        line = raw.decode("utf-8", errors="ignore")
        match = PTS_RE.search(line)
        if match:
            try:
                pts_queue.put(float(match.group(1)))
            except ValueError:
                pass


def stream_frames(
    video: Path,
    start: float,
    duration: float,
    fps: float,
    width: int,
    height: int,
) -> Iterator[tuple[float, np.ndarray]]:
    cmd = [
        "ffmpeg",
        "-hide_banner",
        "-loglevel",
        "info",
        "-nostdin",
        "-ss",
        f"{start:.3f}",
        "-t",
        f"{duration:.3f}",
        "-i",
        str(video),
        "-vf",
        f"fps={fps},scale={width}:{height},showinfo",
        "-pix_fmt",
        "bgr24",
        "-f",
        "rawvideo",
        "pipe:1",
    ]
    proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    assert proc.stdout is not None
    assert proc.stderr is not None
    pts_queue: queue.Queue[float] = queue.Queue()
    threading.Thread(target=collect_pts, args=(proc.stderr, pts_queue), daemon=True).start()
    frame_size = width * height * 3
    index = 0
    base_pts: float | None = None
    try:
        while True:
            data = proc.stdout.read(frame_size)
            if not data:
                break
            if len(data) != frame_size:
                break
            frame = np.frombuffer(data, dtype=np.uint8).reshape((height, width, 3))
            try:
                pts = pts_queue.get(timeout=2)
                if base_pts is None:
                    base_pts = pts
                second = start + max(0.0, pts - base_pts)
            except queue.Empty:
                second = start + index / fps
            yield second, frame
            index += 1
    finally:
        if proc.poll() is None:
            proc.stdout.close()
            proc.terminate()
        proc.wait(timeout=10)


def stream_source_frames(
    video: Path,
    start: float,
    duration: float,
    fps: float,
    width: int,
    height: int,
) -> Iterator[tuple[float, np.ndarray]]:
    cmd = [
        "ffmpeg",
        "-hide_banner",
        "-loglevel",
        "info",
        "-nostdin",
        "-ss",
        f"{start:.3f}",
        "-t",
        f"{duration:.3f}",
        "-i",
        str(video),
        "-vf",
        f"fps={fps},showinfo",
        "-pix_fmt",
        "bgr24",
        "-f",
        "rawvideo",
        "pipe:1",
    ]
    proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    assert proc.stdout is not None
    assert proc.stderr is not None
    pts_queue: queue.Queue[float] = queue.Queue()
    threading.Thread(target=collect_pts, args=(proc.stderr, pts_queue), daemon=True).start()
    frame_size = width * height * 3
    index = 0
    base_pts: float | None = None
    try:
        while True:
            data = proc.stdout.read(frame_size)
            if not data:
                break
            if len(data) != frame_size:
                break
            frame = np.frombuffer(data, dtype=np.uint8).reshape((height, width, 3))
            try:
                pts = pts_queue.get(timeout=2)
                if base_pts is None:
                    base_pts = pts
                second = start + max(0.0, pts - base_pts)
            except queue.Empty:
                second = start + index / fps
            yield second, frame
            index += 1
    finally:
        if proc.poll() is None:
            proc.stdout.close()
            proc.terminate()
        proc.wait(timeout=10)


def write_jpeg_frame(frame: np.ndarray, out: Path) -> None:
    out.parent.mkdir(parents=True, exist_ok=True)
    if not cv2.imwrite(str(out), frame):
        raise RuntimeError(f"failed to write image: {out}")


def load_rois(path: Path) -> dict[str, tuple[float, float, float, float]]:
    raw = json.loads(path.read_text(encoding="utf-8"))
    return {name: tuple(value) for name, value in raw["fields"].items()}


def crop_roi(img: np.ndarray, roi: tuple[float, float, float, float]) -> np.ndarray:
    x1, y1, x2, y2 = roi
    return crop(img, x1, y1, x2, y2)


def build_multipart(files: list[tuple[str, bytes]]) -> tuple[bytes, str]:
    boundary = f"----rm-monitor-{uuid.uuid4().hex}"
    chunks: list[bytes] = []
    for name, data in files:
        content_type = mimetypes.guess_type(name)[0] or "application/octet-stream"
        chunks.extend(
            [
                f"--{boundary}\r\n".encode(),
                f'Content-Disposition: form-data; name="files"; filename="{name}"\r\n'.encode(),
                f"Content-Type: {content_type}\r\n\r\n".encode(),
                data,
                b"\r\n",
            ]
        )
    chunks.append(f"--{boundary}--\r\n".encode())
    return b"".join(chunks), f"multipart/form-data; boundary={boundary}"


def post_ocr(base_url: str, files: list[tuple[str, bytes]], timeout_seconds: float) -> dict:
    url = urllib.parse.urljoin(base_url.rstrip("/") + "/", "ocr")
    body, content_type = build_multipart(files)
    req = urllib.request.Request(url, data=body, method="POST")
    req.add_header("Content-Type", content_type)
    started = time.perf_counter()
    with urllib.request.urlopen(req, timeout=timeout_seconds) as resp:
        payload = json.loads(resp.read().decode("utf-8"))
    payload["client_elapsed_seconds"] = time.perf_counter() - started
    return payload


def parse_int_text(text: str) -> int | None:
    digits = re.sub(r"[^0-9]", "", text or "")
    return int(digits) if digits else None


def parse_ammo_text(text: str) -> dict[str, int | None]:
    match = re.search(r"(\d+)\s*/\s*(\d+)", text or "")
    if not match:
        return {"large": None, "small": None}
    return {"large": int(match.group(1)), "small": int(match.group(2))}


def normalize_ocr_results(payload: dict) -> dict[str, dict]:
    fields: dict[str, dict] = {}
    for item in payload.get("results", []):
        name = Path(item.get("name", "")).stem
        fields[name] = {
            "text": item.get("text", ""),
            "items": item.get("items", []),
        }
    return fields


def build_settlement_data(ocr_fields: dict[str, dict]) -> dict:
    return {
        "red": {
            "score_reference": parse_int_text(ocr_fields.get("red_team_score", {}).get("text", "")),
            "outpost_hp": parse_int_text(ocr_fields.get("red_outpost_hp", {}).get("text", "")),
            "base_hp": parse_int_text(ocr_fields.get("red_base_hp", {}).get("text", "")),
            "economy": parse_int_text(ocr_fields.get("red_economy", {}).get("text", "")),
            "damage": parse_int_text(ocr_fields.get("red_damage", {}).get("text", "")),
            "kills": parse_int_text(ocr_fields.get("red_kills", {}).get("text", "")),
            "ammo": parse_ammo_text(ocr_fields.get("red_ammo", {}).get("text", "")),
        },
        "blue": {
            "score_reference": parse_int_text(ocr_fields.get("blue_team_score", {}).get("text", "")),
            "outpost_hp": parse_int_text(ocr_fields.get("blue_outpost_hp", {}).get("text", "")),
            "base_hp": parse_int_text(ocr_fields.get("blue_base_hp", {}).get("text", "")),
            "economy": parse_int_text(ocr_fields.get("blue_economy", {}).get("text", "")),
            "damage": parse_int_text(ocr_fields.get("blue_damage", {}).get("text", "")),
            "kills": parse_int_text(ocr_fields.get("blue_kills", {}).get("text", "")),
            "ammo": parse_ammo_text(ocr_fields.get("blue_ammo", {}).get("text", "")),
        },
        "victory_condition": ocr_fields.get("victory_condition", {}).get("text", ""),
    }


def ocr_settlement(
    settlement_image: Path,
    roi_path: Path,
    ocr_server_url: str,
    timeout_seconds: float,
    output_path: Path | None = None,
) -> dict:
    img = cv2.imread(str(settlement_image))
    if img is None:
        raise RuntimeError(f"cannot read settlement image: {settlement_image}")

    rois = load_rois(roi_path)
    files: list[tuple[str, bytes]] = []
    for name, roi in rois.items():
        field = crop_roi(img, roi)
        if field.size == 0:
            continue
        ok, buf = cv2.imencode(".jpg", field)
        if ok:
            files.append((f"{name}.jpg", buf.tobytes()))

    response = post_ocr(ocr_server_url, files, timeout_seconds)
    fields = normalize_ocr_results(response)
    result = {
        "schema": "rm-monitor/settlement-ocr/v1",
        "settlement_image": str(settlement_image),
        "roi_schema": str(roi_path),
        "ocr_server_url": ocr_server_url,
        "field_count": len(files),
        "elapsed_seconds": response.get("elapsed_seconds"),
        "client_elapsed_seconds": response.get("client_elapsed_seconds"),
        "fields": fields,
        "data": build_settlement_data(fields),
    }
    if output_path is not None:
        atomic_write_json(output_path, result)
    return result


def extract_best_artifact_frame(
    video: Path,
    center_second: float,
    scorer: Callable[[np.ndarray], tuple[float, dict[str, float]]],
    window_seconds: float,
    fps: float,
    width: int,
    height: int,
    score_width: int,
    score_height: int,
) -> tuple[float, float, dict[str, float], np.ndarray]:
    """Extract the best source-resolution artifact frame around a detector hit.

    The detector works on low-resolution sampled frames. A single timestamp seek
    can land on a transition frame in FLV/HLS recordings, so artifact extraction
    rescans a small source-resolution window around the detector hit and chooses
    the highest scoring frame with the same layout scorer.
    """
    start = max(0.0, center_second - window_seconds / 2)
    best_second = center_second
    best_score = -1.0
    best_details: dict[str, float] = {}
    best_frame: np.ndarray | None = None
    for second, frame in stream_source_frames(video, start, window_seconds, fps, width, height):
        score_frame = cv2.resize(frame, (score_width, score_height), interpolation=cv2.INTER_AREA)
        score, details = scorer(score_frame)
        if score > best_score:
            best_second = second
            best_score = score
            best_details = details
            best_frame = frame.copy()
    if best_frame is None:
        raise RuntimeError("high-resolution settlement refine did not produce frames")
    return best_second, best_score, best_details, best_frame


def segment_to_dict(segment: Segment | None) -> dict:
    if segment is None:
        return {"status": "MISSING"}
    return {
        "status": segment.status,
        "start_seconds": segment.start_seconds,
        "end_seconds": segment.end_seconds,
        "best_seconds": segment.best_seconds,
        "avg_score": segment.avg_score,
        "max_score": segment.max_score,
        "matched_frames": segment.matched_frames,
        "details": segment.details,
    }


def scan_forward(
    video: Path,
    duration: float,
    scorer: Callable[[np.ndarray], tuple[float, dict[str, float]]],
    conf: ScannerConfig,
    fps: float,
    width: int,
    height: int,
    max_scan_seconds: float,
) -> Segment | None:
    tracker = StableSegmentTracker(conf)
    scan_duration = min(duration, max_scan_seconds)
    for second, frame in stream_frames(video, 0.0, scan_duration, fps, width, height):
        score, details = scorer(frame)
        tracker.feed(Detection(second=second, score=score, details=details, frame=frame.copy()))
        if tracker.accepted:
            return tracker.accepted[0]
    segments = tracker.finish()
    return segments[0] if segments else None


def scan_backward_chunks(
    video: Path,
    duration: float,
    scorer: Callable[[np.ndarray], tuple[float, dict[str, float]]],
    conf: ScannerConfig,
    fps: float,
    width: int,
    height: int,
    chunk_seconds: float,
    max_scan_seconds: float,
) -> Segment | None:
    scanned = 0.0
    end = duration
    while end > 0 and scanned < max_scan_seconds:
        chunk = min(chunk_seconds, end, max_scan_seconds - scanned)
        start = max(0.0, end - chunk)
        tracker = StableSegmentTracker(conf)
        for second, frame in stream_frames(video, start, chunk, fps, width, height):
            score, details = scorer(frame)
            tracker.feed(Detection(second=second, score=score, details=details, frame=frame.copy()))
        segments = tracker.finish()
        if segments:
            # This chunk is the latest chunk containing a stable settlement.
            # Use the earliest stable frame in that contiguous segment as the
            # settlement start.
            return segments[0]
        scanned += chunk
        end = start
    return None


def analyze_round(
    video: Path,
    output_dir: Path,
    role: str,
    fps: float,
    width: int,
    height: int,
    max_start_scan_seconds: float,
    max_settlement_scan_seconds: float,
    settlement_chunk_seconds: float,
    min_settlement_second: float,
    min_round_seconds: float,
    settlement_tail_seconds: float,
    settlement_refine_window_seconds: float,
    settlement_refine_fps: float,
    ocr_server_url: str,
    ocr_timeout_seconds: float,
    roi_path: Path,
) -> dict:
    started_at = time.perf_counter()
    duration = ffprobe_duration(video)
    source_width, source_height = ffprobe_dimensions(video)

    with ThreadPoolExecutor(max_workers=2) as pool:
        start_future = pool.submit(
            scan_forward,
            video,
            duration,
            score_pregame,
            PREGAME_CONFIG,
            fps,
            width,
            height,
            max_start_scan_seconds,
        )
        settlement_future = pool.submit(
            scan_backward_chunks,
            video,
            duration,
            score_settlement,
            SETTLEMENT_CONFIG,
            fps,
            width,
            height,
            settlement_chunk_seconds,
            max_settlement_scan_seconds,
        )
        pregame = start_future.result()
        settlement = settlement_future.result()

    elapsed = time.perf_counter() - started_at

    raw_start = segment_to_dict(pregame)
    raw_settlement = segment_to_dict(settlement)

    start_seconds = pregame.start_seconds if pregame else 0.0
    settlement_seconds = settlement.start_seconds if settlement else duration
    settlement_status = "CONFIRMED" if settlement else "INVALID"
    validation_errors: list[str] = []
    if not pregame:
        validation_errors.append("start candidate missing; using source start")
    if not settlement:
        validation_errors.append("settlement candidate missing; using source end")
    elif settlement.start_seconds < min_settlement_second:
        settlement_status = "INVALID"
        validation_errors.append("settlement candidate is too close to source start and likely belongs to previous round")
    if start_seconds + min_round_seconds >= settlement_seconds:
        start_seconds = 0.0
        settlement_seconds = duration
        settlement_status = "INVALID"
        validation_errors.append("start plus minimum round duration is not before settlement; reset boundary to full source")
    elif settlement_status == "CONFIRMED":
        settlement_seconds = min(settlement_seconds + settlement_tail_seconds, duration)

    analysis_status = "CONFIRMED" if settlement_status == "CONFIRMED" else "PARTIAL"

    output_dir.mkdir(parents=True, exist_ok=True)
    start_detect_path = None
    if pregame and pregame.best_frame is not None:
        start_detect_path = output_dir / "start-detect.jpg"
        write_jpeg_frame(pregame.best_frame, start_detect_path)

    settlement_path = None
    settlement_detect_path = None
    refined_settlement = None
    if settlement and settlement_status != "INVALID":
        settlement_path = output_dir / "settlement.jpg"
        if settlement.best_frame is None:
            raise RuntimeError("settlement detector did not retain a matched frame")
        settlement_detect_path = output_dir / "settlement-detect.jpg"
        write_jpeg_frame(settlement.best_frame, settlement_detect_path)
        refined_second, refined_score, refined_details, refined_frame = extract_best_artifact_frame(
            video=video,
            center_second=settlement.best_seconds,
            scorer=score_settlement,
            window_seconds=settlement_refine_window_seconds,
            fps=settlement_refine_fps,
            width=source_width,
            height=source_height,
            score_width=width,
            score_height=height,
        )
        write_jpeg_frame(refined_frame, settlement_path)
        refined_settlement = {
            "second": refined_second,
            "score": refined_score,
            "details": refined_details,
            "window_seconds": settlement_refine_window_seconds,
            "fps": settlement_refine_fps,
            "width": source_width,
            "height": source_height,
        }

    settlement_ocr = None
    if settlement_path and ocr_server_url:
        settlement_ocr = ocr_settlement(
            settlement_image=settlement_path,
            roi_path=roi_path,
            ocr_server_url=ocr_server_url,
            timeout_seconds=ocr_timeout_seconds,
        )

    result = {
        "schema": "rm-monitor/round-analysis/v1",
        "source": str(video),
        "duration_seconds": duration,
        "role": role,
        "scan": {
            "fps": fps,
            "width": width,
            "height": height,
            "max_start_scan_seconds": max_start_scan_seconds,
            "max_settlement_scan_seconds": max_settlement_scan_seconds,
            "settlement_chunk_seconds": settlement_chunk_seconds,
            "min_settlement_second": min_settlement_second,
            "min_round_seconds": min_round_seconds,
            "settlement_tail_seconds": settlement_tail_seconds,
            "settlement_refine_window_seconds": settlement_refine_window_seconds,
            "settlement_refine_fps": settlement_refine_fps,
            "source_width": source_width,
            "source_height": source_height,
            "elapsed_seconds": elapsed,
        },
        "analysis": {
            "status": analysis_status,
            "errors": validation_errors,
        },
        "boundary": {
            "start_seconds": start_seconds,
            "end_seconds": settlement_seconds,
            "duration_seconds": max(0.0, settlement_seconds - start_seconds),
        },
        "settlement": {
            "status": settlement_status,
            "image_path": str(settlement_path) if settlement_path else "",
            "detect_image_path": str(settlement_detect_path) if settlement_detect_path else "",
            "raw": raw_settlement,
            "refined": refined_settlement,
            "ocr": settlement_ocr if settlement_ocr else None,
        },
        "start": {"raw": raw_start, "detect_image_path": str(start_detect_path) if start_detect_path else ""},
        "start_detect_image": str(start_detect_path) if start_detect_path else "",
        "settlement_image": str(settlement_path) if settlement_path else "",
        "settlement_detect_image": str(settlement_detect_path) if settlement_detect_path else "",
        "validation_errors": validation_errors,
    }
    round_json_path = output_dir / "round.json"
    atomic_write_json(round_json_path, result)
    return result


def default_context() -> dict:
    return {
        "schema": "rm-monitor/analyze-job-context/v1",
        "analyze_task_id": 0,
        "match_round_id": 0,
        "source_artifact_id": 0,
        "source_path": "",
        "round_dir": "",
        "role": "主视角",
        "ocr_server_url": "",
        "ocr_timeout_seconds": 30,
        "roi_path": str(DEFAULT_ROI_PATH),
        "scan": {
            "fps": 1.0,
            "width": 320,
            "height": 180,
            "max_start_scan_seconds": 1800,
            "max_settlement_scan_seconds": 1800,
            "settlement_chunk_seconds": 90,
            "min_settlement_second": 60,
            "min_round_seconds": 60,
            "settlement_tail_seconds": 5,
            "settlement_refine_window_seconds": 8,
            "settlement_refine_fps": 3,
        },
    }


def load_context_from_env() -> dict | None:
    raw = None
    # Keep env var name aligned with the shared job contract used by the Go jobs.
    import os

    raw = os.getenv("RM_MONITOR_JOB_CONTEXT")
    if not raw:
        return None
    ctx = default_context()
    user_ctx = json.loads(raw)
    merge_dict(ctx, user_ctx)
    return ctx


def merge_dict(base: dict, override: dict) -> None:
    for key, value in override.items():
        if isinstance(value, dict) and isinstance(base.get(key), dict):
            merge_dict(base[key], value)
        else:
            base[key] = value


def context_from_args(args: argparse.Namespace) -> dict:
    ctx = default_context()
    merge_dict(
        ctx,
        {
            "analyze_task_id": 0,
            "source_path": str(args.source),
            "round_dir": str(args.output_dir),
            "role": args.role,
            "ocr_server_url": args.ocr_server_url,
            "ocr_timeout_seconds": args.ocr_timeout_seconds,
            "roi_path": str(args.roi),
            "scan": {
                "fps": args.fps,
                "width": args.width,
                "height": args.height,
                "max_start_scan_seconds": args.max_start_scan_seconds,
                "max_settlement_scan_seconds": args.max_settlement_scan_seconds,
                "settlement_chunk_seconds": args.settlement_chunk_seconds,
                "min_settlement_second": args.min_settlement_second,
                "min_round_seconds": args.min_round_seconds,
                "settlement_tail_seconds": args.settlement_tail_seconds,
                "settlement_refine_window_seconds": args.settlement_refine_window_seconds,
                "settlement_refine_fps": args.settlement_refine_fps,
            },
        },
    )
    return ctx


def job_dir(round_dir: Path, job_name: str) -> Path:
    return round_dir / ".job" / job_name


def run_job(ctx: dict) -> dict:
    source = Path(ctx["source_path"])
    round_dir = Path(ctx["round_dir"])
    analyze_task_id = int(ctx.get("analyze_task_id") or 0)
    match_round_id = int(ctx.get("match_round_id") or 0)
    source_artifact_id = int(ctx.get("source_artifact_id") or 0)
    name = f"analyze-{analyze_task_id}" if analyze_task_id > 0 else (ctx.get("job_name") or f"analyze-{int(time.time())}")
    work_dir = job_dir(round_dir, name)
    work_dir.mkdir(parents=True, exist_ok=True)
    atomic_write_json(work_dir / "context.json", ctx)
    try:
        scan = ctx["scan"]
        result = analyze_round(
            video=source,
            output_dir=round_dir,
            role=ctx.get("role", "主视角"),
            fps=float(scan["fps"]),
            width=int(scan["width"]),
            height=int(scan["height"]),
            max_start_scan_seconds=float(scan["max_start_scan_seconds"]),
            max_settlement_scan_seconds=float(scan["max_settlement_scan_seconds"]),
            settlement_chunk_seconds=float(scan["settlement_chunk_seconds"]),
            min_settlement_second=float(scan["min_settlement_second"]),
            min_round_seconds=float(scan["min_round_seconds"]),
            settlement_tail_seconds=float(scan["settlement_tail_seconds"]),
            settlement_refine_window_seconds=float(scan["settlement_refine_window_seconds"]),
            settlement_refine_fps=float(scan["settlement_refine_fps"]),
            ocr_server_url=ctx.get("ocr_server_url", ""),
            ocr_timeout_seconds=float(ctx.get("ocr_timeout_seconds", 30)),
            roi_path=Path(ctx.get("roi_path") or DEFAULT_ROI_PATH),
        )
        settlement_status = result.get("settlement", {}).get("status") or "INVALID"
        boundary = result.get("boundary", {})
        round_json_path = round_dir / "round.json"
        settlement_path = result.get("settlement", {}).get("image_path", "")
        job_result = {
            "schema": "rm-monitor/analyze-result/v1",
            "analyze_task_id": analyze_task_id,
            "match_round_id": match_round_id,
            "source_artifact_id": source_artifact_id,
            "round_json_path": str(round_json_path),
            "settlement_image_path": settlement_path,
            "settlement_status": settlement_status,
            "effective_start_seconds": float(boundary.get("start_seconds") or 0.0),
            "effective_end_seconds": float(boundary.get("end_seconds") or 0.0),
            "completed_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        }
        atomic_write_json(work_dir / "result.json", job_result)
        return job_result
    except Exception as exc:
        error = {
            "schema": "rm-monitor/job-error/v1",
            "task_type": "analyze",
            "task_id": analyze_task_id,
            "status": "FAILED",
            "error_message": str(exc),
            "completed_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        }
        atomic_write_json(work_dir / "error.json", error)
        raise


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", type=Path)
    parser.add_argument("--output-dir", type=Path)
    parser.add_argument("--job-name", default="")
    parser.add_argument("--role", default="主视角")
    parser.add_argument("--fps", type=float, default=1.0)
    parser.add_argument("--width", type=int, default=320)
    parser.add_argument("--height", type=int, default=180)
    parser.add_argument("--max-start-scan-seconds", type=float, default=1800)
    parser.add_argument("--max-settlement-scan-seconds", type=float, default=1800)
    parser.add_argument("--settlement-chunk-seconds", type=float, default=90)
    parser.add_argument("--min-settlement-second", type=float, default=60)
    parser.add_argument("--min-round-seconds", type=float, default=60)
    parser.add_argument("--settlement-tail-seconds", type=float, default=5)
    parser.add_argument("--settlement-refine-window-seconds", type=float, default=8)
    parser.add_argument("--settlement-refine-fps", type=float, default=3)
    parser.add_argument("--ocr-server-url", default="")
    parser.add_argument("--ocr-timeout-seconds", type=float, default=30)
    parser.add_argument("--roi", type=Path, default=DEFAULT_ROI_PATH)
    args = parser.parse_args()

    ctx = load_context_from_env()
    if ctx is None:
        if args.source is None or args.output_dir is None:
            raise SystemExit("--source and --output-dir are required when RM_MONITOR_JOB_CONTEXT is not set")
        ctx = context_from_args(args)
    result = run_job(ctx)
    print(json.dumps(result, ensure_ascii=False))


if __name__ == "__main__":
    main()

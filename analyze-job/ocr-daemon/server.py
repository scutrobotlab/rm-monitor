from __future__ import annotations

import io
import os
import time
from typing import Any

import numpy as np
from fastapi import FastAPI, File, HTTPException, UploadFile
from PIL import Image
from paddleocr import PaddleOCR


app = FastAPI(title="rm-monitor PP-OCRv5 daemon", version="0.1.0")
ocr: PaddleOCR | None = None
started_at = time.time()


def env_bool(name: str, default: bool) -> bool:
    value = os.getenv(name)
    if value is None:
        return default
    return value.lower() in {"1", "true", "yes", "on"}


def create_ocr() -> PaddleOCR:
    device = os.getenv("OCR_DEVICE", "cpu")
    lang = os.getenv("OCR_LANG", "ch")
    version = os.getenv("OCR_VERSION", "PP-OCRv5")
    enable_mkldnn = env_bool("OCR_ENABLE_MKLDNN", True)
    cpu_threads = int(os.getenv("OCR_CPU_THREADS", "4"))

    # PaddleOCR 3.x uses predict(); these flags keep the service focused on
    # small settlement ROI crops and avoid document orientation/table pipelines.
    return PaddleOCR(
        lang=lang,
        ocr_version=version,
        device=device,
        enable_mkldnn=enable_mkldnn,
        cpu_threads=cpu_threads,
        use_doc_orientation_classify=False,
        use_doc_unwarping=False,
        use_textline_orientation=False,
    )


@app.on_event("startup")
def startup() -> None:
    global ocr
    ocr = create_ocr()


@app.get("/health")
def health() -> dict[str, Any]:
    return {
        "ok": ocr is not None,
        "uptime_seconds": time.time() - started_at,
        "device": os.getenv("OCR_DEVICE", "cpu"),
        "version": os.getenv("OCR_VERSION", "PP-OCRv5"),
    }


def decode_image(raw: bytes) -> np.ndarray:
    try:
        image = Image.open(io.BytesIO(raw)).convert("RGB")
    except Exception as exc:
        raise HTTPException(status_code=400, detail=f"invalid image: {exc}") from exc
    return np.asarray(image)


def normalize_prediction(prediction: Any) -> list[dict[str, Any]]:
    items: list[dict[str, Any]] = []

    # PaddleOCR 3.x returns OCRResult objects with json/dict-like payloads.
    if hasattr(prediction, "json"):
        payload = prediction.json
    elif isinstance(prediction, dict):
        payload = prediction
    else:
        payload = getattr(prediction, "__dict__", {})
    if isinstance(payload, dict) and isinstance(payload.get("res"), dict):
        payload = payload["res"]

    texts = payload.get("rec_texts") or payload.get("texts") or []
    scores = payload.get("rec_scores") or payload.get("scores") or []
    boxes = payload.get("rec_polys") or payload.get("dt_polys") or payload.get("boxes") or []

    for idx, text in enumerate(texts):
        items.append(
            {
                "text": str(text),
                "confidence": float(scores[idx]) if idx < len(scores) else None,
                "box": normalize_box(boxes[idx]) if idx < len(boxes) else None,
            }
        )
    return items


def normalize_box(box: Any) -> Any:
    if hasattr(box, "tolist"):
        return box.tolist()
    if isinstance(box, tuple):
        return list(box)
    return box


@app.post("/ocr")
async def recognize(files: list[UploadFile] = File(...)) -> dict[str, Any]:
    if ocr is None:
        raise HTTPException(status_code=503, detail="ocr model is not ready")
    max_batch = int(os.getenv("OCR_MAX_BATCH", "32"))
    if len(files) > max_batch:
        raise HTTPException(status_code=400, detail=f"too many files: {len(files)} > {max_batch}")

    started = time.perf_counter()
    images = [decode_image(await file.read()) for file in files]
    predictions = ocr.predict(images)
    results = []
    for file, prediction in zip(files, predictions):
        items = normalize_prediction(prediction)
        results.append(
            {
                "name": file.filename,
                "text": "".join(item["text"] for item in items),
                "items": items,
            }
        )
    return {
        "results": results,
        "elapsed_seconds": time.perf_counter() - started,
    }

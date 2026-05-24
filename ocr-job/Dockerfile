FROM python:3.11-slim

WORKDIR /app

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata ffmpeg libglib2.0-0 libgl1 libgomp1 \
    && rm -rf /var/lib/apt/lists/*

COPY ocr-job/requirements.txt ./requirements.txt
RUN pip install --no-cache-dir --upgrade pip \
    && pip install --no-cache-dir torch==2.3.1 torchvision==0.18.1 --index-url https://download.pytorch.org/whl/cpu \
    && pip install --no-cache-dir -r requirements.txt \
    && python -c "import easyocr; easyocr.Reader(['ch_sim','en'], gpu=False)"

COPY ocr-job/main.py ocr-job/ocr_engine.py ocr-job/roi_template.json ./

ENTRYPOINT ["python", "/app/main.py"]

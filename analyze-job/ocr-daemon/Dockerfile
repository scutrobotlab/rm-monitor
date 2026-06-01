FROM python:3.10-slim

ARG PADDLE_DEVICE=cpu

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1

WORKDIR /app

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
      libglib2.0-0 \
      libgl1 \
      libgomp1 \
      curl \
    && rm -rf /var/lib/apt/lists/*

COPY analyze-job/ocr-daemon/requirements.txt /app/requirements.txt
RUN python -m pip install --upgrade pip \
    && if [ "$PADDLE_DEVICE" = "gpu" ]; then \
      python -m pip install paddlepaddle-gpu==3.0.0; \
    else \
      python -m pip install paddlepaddle==3.0.0; \
    fi \
    && python -m pip install -r /app/requirements.txt

COPY analyze-job/ocr-daemon/server.py /app/server.py

EXPOSE 8000
CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8000"]

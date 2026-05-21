# syntax=docker/dockerfile:1.7
FROM node:22-bookworm-slim AS deps
WORKDIR /app/danmu-job
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    ca-certificates \
    libcairo2-dev \
    libgif-dev \
    libjpeg-dev \
    libpango1.0-dev \
    librsvg2-dev \
    pkg-config \
    python3 \
  && rm -rf /var/lib/apt/lists/*
COPY danmu-job/package.json danmu-job/package-lock.json* ./
RUN --mount=type=cache,target=/root/.npm npm install

FROM deps AS builder
COPY danmu-job/tsconfig.json ./
COPY danmu-job/src ./src
RUN npm run build && npm prune --omit=dev

FROM node:22-bookworm-slim
ENV TZ=Asia/Shanghai
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    fonts-noto-cjk \
    libcairo2 \
    libgif7 \
    libjpeg62-turbo \
    libpango-1.0-0 \
    libpangocairo-1.0-0 \
    librsvg2-2 \
    tzdata \
  && rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/danmu-job/node_modules ./node_modules
COPY --from=builder /app/danmu-job/dist ./dist
ENTRYPOINT ["node", "/app/dist/main.js"]

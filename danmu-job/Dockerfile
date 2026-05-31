# syntax=docker/dockerfile:1.7
FROM oven/bun:1.2.15-debian AS deps
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
RUN --mount=type=cache,target=/root/.bun/install/cache bun install --production

FROM deps AS builder
COPY danmu-job/tsconfig.json ./
COPY danmu-job/src ./src
RUN bun build ./src/main.ts --target=bun --format=esm --outfile=dist/main.js --external canvas
RUN mkdir -p /app/runtime/node_modules \
  && cp -a node_modules/canvas /app/runtime/node_modules/canvas

FROM oven/bun:1.2.15-debian
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
COPY --from=builder /app/runtime/node_modules ./node_modules
COPY --from=builder /app/danmu-job/dist ./dist
ENTRYPOINT ["bun", "/app/dist/main.js"]

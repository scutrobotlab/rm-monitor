# syntax=docker/dockerfile:1.7
FROM rust:1.88-bookworm AS builder
WORKDIR /app/highlight-artifact-job
COPY highlight-artifact-job/Cargo.toml highlight-artifact-job/Cargo.lock* ./
COPY highlight-artifact-job/src ./src
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    --mount=type=cache,target=/app/highlight-artifact-job/target \
    cargo build --release \
    && cp /app/highlight-artifact-job/target/release/highlight-artifact-job /usr/local/bin/highlight-artifact-job

FROM debian:bookworm-slim

ENV TZ=Asia/Shanghai
WORKDIR /app
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata ffmpeg fonts-noto-cjk \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /usr/local/bin/highlight-artifact-job /usr/local/bin/highlight-artifact-job

ENTRYPOINT ["/usr/local/bin/highlight-artifact-job"]

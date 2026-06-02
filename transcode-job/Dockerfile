# syntax=docker/dockerfile:1.7
FROM rust:1.85-bookworm AS builder

WORKDIR /app
COPY transcode-job/Cargo.toml transcode-job/Cargo.lock ./transcode-job/
COPY transcode-job/src ./transcode-job/src
WORKDIR /app/transcode-job
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/app/transcode-job/target \
    cargo build --release --locked && cp target/release/transcode-job /usr/local/bin/transcode-job

FROM debian:bookworm-slim

ENV TZ=Asia/Shanghai
WORKDIR /app
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && ffmpeg -hide_banner -encoders 2>/dev/null | grep -q libsvtav1

COPY --from=builder /usr/local/bin/transcode-job /usr/local/bin/transcode-job

ENTRYPOINT ["/usr/local/bin/transcode-job"]

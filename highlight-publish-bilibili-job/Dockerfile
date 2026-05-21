FROM rust:1.88-bookworm AS builder
WORKDIR /app/highlight-publish-bilibili-job
COPY highlight-publish-bilibili-job/Cargo.toml highlight-publish-bilibili-job/Cargo.lock ./
COPY highlight-publish-bilibili-job/src ./src
RUN cargo build --release

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata ffmpeg fonts-noto-cjk \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=builder /app/highlight-publish-bilibili-job/target/release/highlight-publish-bilibili-job /usr/local/bin/highlight-publish-bilibili-job
ENTRYPOINT ["/usr/local/bin/highlight-publish-bilibili-job"]

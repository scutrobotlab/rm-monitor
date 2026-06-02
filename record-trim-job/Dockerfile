FROM rust:1.85-bookworm AS builder
WORKDIR /app
COPY record-trim-job/Cargo.toml record-trim-job/Cargo.lock ./record-trim-job/
COPY record-trim-job/src ./record-trim-job/src
WORKDIR /app/record-trim-job
RUN cargo build --release --locked

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata ffmpeg \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/record-trim-job/target/release/record-trim-job /usr/local/bin/record-trim-job
ENTRYPOINT ["/usr/local/bin/record-trim-job"]

# --- Stage 0: External FFmpeg source ---
FROM docker.io/mwader/static-ffmpeg:7.1 AS ffmpeg-source

# --- Stage 1: Build the Go binary ---
FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o nas-proxy-generator ./cmd/builder

# --- Stage 2: Distroless Debian with FFmpeg & GPU support ---
FROM gcr.io/distroless/base-debian12:debug

# Copy system dependencies and ffmpeg from a debian stage or install them. 
# Since distroless:debug has a busybox shell, we can copy over a static/dynamic ffmpeg build 
# or use a multi-stage approach pulling ffmpeg binaries.
COPY --from=ffmpeg-source /ffmpeg /usr/local/bin/
COPY --from=ffmpeg-source /ffprobe /usr/local/bin/

WORKDIR /app

# Copy the compiled Go binary
COPY --from=builder /app/nas-proxy-generator /app/nas-proxy-generator

ENTRYPOINT ["/app/nas-proxy-generator"]

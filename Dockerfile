# syntax=docker/dockerfile:1
# Production multi-stage Dockerfile for Antares (PLAN P10.1)

FROM golang:1.27.1-alpine AS builder

WORKDIR /src

# Cache dependency layer
COPY go.mod go.sum ./
RUN go mod download

# Copy source tree and compile minimal static binaries
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/antares ./cmd/antares \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/observatory ./cmd/observatory

# Runtime stage: unprivileged, minimal attack surface
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata \
 && adduser -D -u 10001 -g antares antares \
 && mkdir -p /data && chown -R antares:antares /data

COPY --from=builder /out/antares /usr/local/bin/antares
COPY --from=builder /out/observatory /usr/local/bin/observatory

USER 10001
WORKDIR /data
VOLUME ["/data"]

EXPOSE 6380

HEALTHCHECK --interval=2s --timeout=1s --retries=5 \
  CMD ["/usr/local/bin/antares", "--version"]

ENTRYPOINT ["/usr/local/bin/antares"]
CMD ["-bind", "0.0.0.0", "-port", "6380", "-appendonly", "yes", "-aof-file", "/data/appendonly.aof"]

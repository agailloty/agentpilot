# syntax=docker/dockerfile:1

FROM node:22-alpine AS web-builder

WORKDIR /src/web

COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY server/go.mod ./server/
COPY server/ ./server/
COPY docs/AgentManifests ./docs/AgentManifests

WORKDIR /src/server
RUN if [ -f go.sum ]; then go mod download; fi && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/agentpilot-server ./cmd/server

RUN mkdir -p /out/data && chown 65532:65532 /out/data

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=builder /out/agentpilot-server /usr/local/bin/agentpilot-server
COPY --from=builder --chown=65532:65532 /src/docs/AgentManifests /app/manifests
COPY --from=web-builder --chown=65532:65532 /src/web/dist /app/web
COPY --from=builder --chown=65532:65532 /out/data /data

ENV AGENTPILOT_LISTEN_ADDR=0.0.0.0:8080 \
    AGENTPILOT_DATA_DIR=/data \
    AGENTPILOT_MANIFESTS_DIR=/app/manifests \
    AGENTPILOT_WEB_DIR=/app/web \
    AGENTPILOT_APPROVAL_IDLE_MS=1500

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/agentpilot-server"]

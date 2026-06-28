# Multi-stage build. Produces three targets:
#   server  — control plane with the Next.js UI embedded
#   agent   — in-cluster agent
#   cli     — operator CLI

# Stage 1: Build UI static export
FROM node:22-alpine AS ui-builder
WORKDIR /app
COPY ui/package*.json ./
# package-lock.json is committed, so use `npm ci` for reproducible builds.
RUN npm ci --no-audit --no-fund --loglevel=error
COPY ui/ .
RUN npm run build
# Output: /app/out/

# Stage 2: Build Go binaries with embedded UI
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY go.sum ./
RUN go mod download
COPY . .

# Replace the dev placeholder with the real UI export before compiling, so
# //go:embed all:dist picks up the built assets.
RUN rm -rf ./internal/ui/dist && mkdir -p ./internal/ui/dist
COPY --from=ui-builder /app/out/ ./internal/ui/dist/

ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o /lotsman-server ./cmd/server
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o /lotsman-agent ./cmd/agent
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o /lotsman ./cmd/lotsman

# Control-plane image (includes embedded UI)
FROM alpine:3.24 AS server
RUN apk add --no-cache ca-certificates
COPY --from=builder /lotsman-server /usr/local/bin/lotsman-server
EXPOSE 8080 9090
ENTRYPOINT ["lotsman-server"]

# Agent image
FROM alpine:3.24 AS agent
RUN apk add --no-cache ca-certificates
COPY --from=builder /lotsman-agent /usr/local/bin/lotsman-agent
ENTRYPOINT ["lotsman-agent"]

# CLI image
FROM alpine:3.24 AS cli
RUN apk add --no-cache ca-certificates
COPY --from=builder /lotsman /usr/local/bin/lotsman
ENTRYPOINT ["lotsman"]

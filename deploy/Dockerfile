# xDocker container image (linux/amd64 for the RS3621RPxs; arm64 also builds).
# Build from the repository root:  docker build -f deploy/Dockerfile -t xdocker .

FROM node:20-alpine AS ui
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
# vite writes into ../backend/internal/web/dist
RUN mkdir -p /src/backend/internal/web && npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=ui /src/backend/internal/web/dist ./internal/web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/xdocker ./cmd/xdocker

FROM alpine:3.22
# docker-cli + compose plugin: stacks are deployed with `docker compose`,
# bundled here instead of relying on DSM's copy (PLAN.md risk #2).
RUN apk add --no-cache ca-certificates tzdata docker-cli docker-cli-compose
COPY --from=build /out/xdocker /usr/local/bin/xdocker
ENV XDOCKER_DATA_DIR=/data \
    XDOCKER_LISTEN=:8080
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/xdocker"]

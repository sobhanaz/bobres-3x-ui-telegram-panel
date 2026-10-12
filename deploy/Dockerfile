# One Dockerfile for every Go service: docker build --build-arg SERVICE=core -f deploy/Dockerfile .
# The backup service has its own target (it needs pg_dump):
#   docker build --target backup --build-arg SERVICE=backup -f deploy/Dockerfile .
# The last stage stays the default, so builds without --target are unchanged.

# The web dashboard: static files that core embeds (web/dashboard/embed.go). Built
# on the build machine's own platform, since the files are the same for every
# target: a multi-arch release does not run npm under emulation.
FROM --platform=$BUILDPLATFORM node:24-alpine AS dashboard
WORKDIR /web
COPY web/dashboard/package.json web/dashboard/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/dashboard/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
ARG SERVICE
ARG VERSION=dev
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=dashboard /web/dist/app ./web/dashboard/dist/app
RUN test -n "$SERVICE" && go build -trimpath \
    -ldflags "-s -w -X github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version.Version=${VERSION}" \
    -o /out/app ./cmd/${SERVICE}

# Minimal runtime from Docker Hub (gcr.io/distroless is blocked in some regions and would
# break builds for customers there). Static binary + CA certs + tzdata only; no shell is
# used at runtime: the binary implements `app --healthcheck` itself.
FROM alpine:3.24 AS runtime-base
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S -g 65532 bobres && adduser -S -u 65532 -G bobres -H -s /sbin/nologin bobres

# The backup service: pg_dump from the same major version as the database (pg_dump
# must be at least the server's), plus the Go /app. It runs as the same non-root uid
# as the other images; the installer gives that uid the host's backups directory.
FROM postgres:16-alpine AS backup
RUN addgroup -S -g 65532 bobres && adduser -S -u 65532 -G bobres -H -s /sbin/nologin bobres \
 && mkdir -p /backups && chown 65532:65532 /backups && chmod 700 /backups
COPY --from=build /out/app /app
USER 65532:65532
ENV BOBRES_BACKUP_DIR=/backups
EXPOSE 8080
ENTRYPOINT ["/app"]
CMD []

FROM scratch
COPY --from=runtime-base /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=runtime-base /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=runtime-base /etc/passwd /etc/passwd
COPY --from=runtime-base /etc/group /etc/group
COPY --from=build /out/app /app
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app"]

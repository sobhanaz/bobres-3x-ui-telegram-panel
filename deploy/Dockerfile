# One Dockerfile for every Go service: docker build --build-arg SERVICE=core -f deploy/Dockerfile .
# The backup service has its own target (it needs pg_dump):
#   docker build --target backup --build-arg SERVICE=backup -f deploy/Dockerfile .
# The last stage stays the default, so builds without --target are unchanged.
# The vendor's license server builds the same way (SERVICE=licensesrv): deploy/vendor/docker-compose.yml.

# The web dashboard: static files that core embeds (web/dashboard/embed.go). Built
# on the build machine's own platform, since the files are the same for every
# target: a multi-arch release does not run npm under emulation.
FROM --platform=$BUILDPLATFORM node:24-alpine AS dashboard
WORKDIR /web
COPY web/dashboard/package.json web/dashboard/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/dashboard/ ./
RUN npm run build

# The Telegram Mini App store, built the same way (web/miniapp/embed.go).
FROM --platform=$BUILDPLATFORM node:24-alpine AS miniapp
WORKDIR /web
COPY web/miniapp/package.json web/miniapp/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/miniapp/ ./
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
COPY --from=miniapp /web/dist/app ./web/miniapp/dist/app
RUN test -n "$SERVICE" && go build -trimpath \
    -ldflags "-s -w -X github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version.Version=${VERSION}" \
    -o /out/app ./cmd/${SERVICE}

# Minimal runtime from Docker Hub (gcr.io/distroless is blocked in some regions and would
# break builds for customers there). Static binary + CA certs + tzdata only; no shell is
# used at runtime: the binary implements `app --healthcheck` itself.
FROM alpine:3.24 AS runtime-base
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S -g 65532 bobres && adduser -S -u 65532 -G bobres -H -s /sbin/nologin bobres \
 && install -d -o 65532 -g 65532 -m 0700 /request /result /tls-result

# The backup service: pg_dump from the same major version as the database (pg_dump
# must be at least the server's), plus the Go /app. It runs as the same non-root uid
# as the other images; the installer gives that uid the host's backups directory.
FROM postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873 AS backup
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
# The mount points of the tls-agent's volumes (core writes its HTTPS request to /request and reads the
# answer from /tls-result; the tls-agent, from this same image, reads /request and writes /result). A
# new named volume takes the owner and mode of the image's directory at its mount point; without these
# the volumes would belong to root and core, as 65532, could not write its request. This is a scratch
# image: there is no shell to make them later.
COPY --from=runtime-base --chown=65532:65532 --chmod=0700 /request /request
COPY --from=runtime-base --chown=65532:65532 --chmod=0700 /result /result
COPY --from=runtime-base --chown=65532:65532 --chmod=0700 /tls-result /tls-result
COPY --from=build /out/app /app
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app"]

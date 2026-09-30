# One Dockerfile for all four Go services: docker build --build-arg SERVICE=core -f deploy/Dockerfile .
FROM golang:1.25-alpine AS build
ARG SERVICE
ARG VERSION=dev
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN test -n "$SERVICE" && go build -trimpath \
    -ldflags "-s -w -X github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version.Version=${VERSION}" \
    -o /out/app ./cmd/${SERVICE}

# Minimal runtime from Docker Hub (gcr.io/distroless is blocked in some regions and would
# break builds for customers there). Static binary + CA certs + tzdata only; no shell is
# used at runtime: the binary implements `app --healthcheck` itself.
FROM alpine:3.20 AS runtime-base
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S -g 65532 bobres && adduser -S -u 65532 -G bobres -H -s /sbin/nologin bobres

FROM scratch
COPY --from=runtime-base /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=runtime-base /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=runtime-base /etc/passwd /etc/passwd
COPY --from=runtime-base /etc/group /etc/group
COPY --from=build /out/app /app
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app"]

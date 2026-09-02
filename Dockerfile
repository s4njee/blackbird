# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26
ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts
COPY web/ ./
RUN npm run build

FROM golang:${GO_VERSION}-alpine AS builder
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=frontend /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" -o /out/blackbird ./cmd/blackbird

FROM alpine:3.22 AS runtime
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="Blackbird" \
      org.opencontainers.image.description="rTorrent web console" \
      org.opencontainers.image.source="https://github.com/blackbird/blackbird" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.licenses="NOASSERTION"

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 65532 blackbird \
    && adduser -S -D -H -u 65532 -G blackbird blackbird \
    && mkdir -p /config /downloads /watch \
    && chown -R 65532:65532 /config /downloads /watch
COPY --from=builder /out/blackbird /usr/local/bin/blackbird
COPY internal/config/example.yml /usr/share/blackbird/example.yml
COPY THIRD_PARTY_NOTICES.md /usr/share/licenses/blackbird/THIRD_PARTY_NOTICES.md
COPY blackbird-entrypoint.sh /usr/local/bin/blackbird-entrypoint
RUN chmod 0755 /usr/local/bin/blackbird-entrypoint

USER 65532:65532
WORKDIR /config
EXPOSE 8222
HEALTHCHECK --interval=15s --timeout=4s --start-period=15s --retries=5 \
  CMD wget -qO- http://127.0.0.1:8222/healthz | grep -q '"ok":true' || exit 1
ENTRYPOINT ["/usr/local/bin/blackbird-entrypoint"]
CMD ["-config", "/config/config.yml"]

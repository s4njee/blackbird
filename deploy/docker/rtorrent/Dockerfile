# syntax=docker/dockerfile:1.7

ARG RTORRENT_VERSION=0.15.7
ARG LIBTORRENT_VERSION=0.15.7
ARG RTORRENT_SHA256=f5f382b340ecdb581236653924a5f37eb65e127df415e79c74a6326146d6a5f8
ARG LIBTORRENT_SHA256=3b2edcb5b89c2a316af562303479e7e14c66c19fed4f8ae794f9383499cced07

FROM debian:bookworm-slim AS builder
ARG RTORRENT_VERSION
ARG LIBTORRENT_VERSION
ARG RTORRENT_SHA256
ARG LIBTORRENT_SHA256
ENV PREFIX=/opt/rtorrent

# Compose may run this service as the host UID so bind-mounted downloads remain
# user-owned. New named volumes inherit the writable /data directory modes,
# allowing that UID to use the session and watch paths too.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       autoconf automake build-essential ca-certificates curl file \
       libcurl4-openssl-dev libncursesw5-dev libssl-dev libtool pkg-config \
       xz-utils zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
RUN curl -fsSL "https://github.com/rakshasa/rtorrent/releases/download/v${LIBTORRENT_VERSION}/libtorrent-${LIBTORRENT_VERSION}.tar.gz" -o libtorrent.tar.gz \
    && echo "${LIBTORRENT_SHA256}  libtorrent.tar.gz" | sha256sum -c - \
    && curl -fsSL "https://github.com/rakshasa/rtorrent/releases/download/v${RTORRENT_VERSION}/rtorrent-${RTORRENT_VERSION}.tar.gz" -o rtorrent.tar.gz \
    && echo "${RTORRENT_SHA256}  rtorrent.tar.gz" | sha256sum -c - \
    && tar -xzf libtorrent.tar.gz \
    && tar -xzf rtorrent.tar.gz

WORKDIR /src/libtorrent-${LIBTORRENT_VERSION}
RUN autoreconf -ivf \
    && ./configure --prefix="${PREFIX}" --disable-debug --disable-static --enable-shared \
    && make -j"$(nproc)" \
    && make install

WORKDIR /src/rtorrent-${RTORRENT_VERSION}
ENV PKG_CONFIG_PATH=/opt/rtorrent/lib/pkgconfig
RUN autoreconf -ivf \
    && ./configure --prefix="${PREFIX}" --disable-debug --disable-static --enable-shared --with-xmlrpc-tinyxml2 \
    && make -j"$(nproc)" \
    && make install

FROM debian:bookworm-slim AS runtime
ARG RTORRENT_VERSION
ARG LIBTORRENT_VERSION
LABEL org.opencontainers.image.title="Blackbird rTorrent" \
      org.opencontainers.image.description="Pinned rTorrent daemon for the Blackbird appliance" \
      org.opencontainers.image.source="https://github.com/rakshasa/rtorrent" \
      org.opencontainers.image.version="${RTORRENT_VERSION}" \
      org.opencontainers.image.licenses="GPL-2.0-or-later"

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       ca-certificates libcurl4 libncursesw6 libstdc++6 netcat-openbsd zlib1g \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system --gid 1000 rtorrent \
    && useradd --system --uid 1000 --gid 1000 --home-dir /data --no-create-home rtorrent \
    && mkdir -p /downloads /data/watch/load /data/watch/start /data/session /etc/rtorrent \
    && chown -R 1000:1000 /data \
    && chmod -R 0777 /data \
    && chmod 0777 /downloads

COPY --from=builder /opt/rtorrent/bin/rtorrent /usr/local/bin/rtorrent
COPY --from=builder /opt/rtorrent/lib/ /usr/local/lib/
COPY deploy/docker/rtorrent/rtorrent.rc /etc/rtorrent/rtorrent.rc
COPY deploy/docker/rtorrent/healthcheck.sh /usr/local/bin/rtorrent-healthcheck
COPY deploy/docker/rtorrent/entrypoint.sh /usr/local/bin/rtorrent-entrypoint
RUN ldconfig \
	&& chmod 0755 /usr/local/bin/rtorrent-healthcheck \
	&& chmod 0755 /usr/local/bin/rtorrent-entrypoint \
    && printf 'rTorrent %s; libtorrent %s\n' "${RTORRENT_VERSION}" "${LIBTORRENT_VERSION}" > /usr/share/doc/rtorrent-blackbird-version

# No USER directive: the entrypoint starts as root to repair mount-point
# ownership, then drops to PUID:PGID (default 1000:1000) via setpriv before
# rTorrent runs. See entrypoint.sh.
WORKDIR /data
VOLUME ["/downloads", "/data/watch", "/data/session"]
EXPOSE 5000
HEALTHCHECK --interval=10s --timeout=5s --start-period=20s --retries=10 \
  CMD ["/usr/local/bin/rtorrent-healthcheck"]
ENTRYPOINT ["/usr/local/bin/rtorrent-entrypoint"]
CMD ["-n", "-o", "import=/etc/rtorrent/rtorrent.rc"]

# ========================================================
# Stage: Frontend (Vite)
# ========================================================
FROM --platform=$BUILDPLATFORM node:26-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
COPY internal/web/translation /src/internal/web/translation
RUN npm run build

# ========================================================
# Stage: Builder
# ========================================================
FROM golang:1.27-alpine AS builder
WORKDIR /app
ARG TARGETARCH
ARG TARGETVARIANT
ARG MIKROZIA_VERSION
ARG XRAY_VERSION
ARG MTG_VERSION

COPY MIKROZIA_VERSION ./MIKROZIA_VERSION
COPY scripts/build/verify-mikrozia-version.sh ./verify-mikrozia-version.sh
RUN sh ./verify-mikrozia-version.sh "${MIKROZIA_VERSION}" > /tmp/mikrozia-version

RUN test -n "${XRAY_VERSION}" && echo "${XRAY_VERSION}" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || { echo "ERROR: XRAY_VERSION build-arg is required (e.g. --build-arg XRAY_VERSION=\$(tr -d '[:space:]' < XRAY_VERSION))" >&2; exit 1; }
RUN test -n "${MTG_VERSION}" && echo "${MTG_VERSION}" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || { echo "ERROR: MTG_VERSION build-arg is required (e.g. --build-arg MTG_VERSION=\$(tr -d '[:space:]' < MTG_VERSION))" >&2; exit 1; }

RUN apk --no-cache --update add \
  build-base \
  gcc \
  curl \
  unzip \
  ca-certificates

COPY . .
COPY --from=frontend /src/internal/web/dist ./internal/web/dist

ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-D_LARGEFILE64_SOURCE"
RUN go build -ldflags "-w -s -X 'github.com/mhsanaei/3x-ui/v3/internal/config.mikroziaVersion=$(cat /tmp/mikrozia-version)'" -o build/x-ui main.go
RUN ./DockerInit.sh "$TARGETARCH" "$TARGETVARIANT" "$XRAY_VERSION" "$MTG_VERSION"

# ========================================================
# Stage: Final Image of 3x-ui
# ========================================================
FROM alpine:3.23.8@sha256:469c9ea410b9215eaee7976ba6c0639039b444be8e5d638f7abbada189971eb2
ARG MIKROZIA_VERSION
ARG XRAY_VERSION
ARG MTG_VERSION
COPY --from=builder /tmp/mikrozia-version /tmp/mikrozia-version
RUN test "$(cat /tmp/mikrozia-version)" = "${MIKROZIA_VERSION}" || { echo "ERROR: OCI label MIKROZIA_VERSION '${MIKROZIA_VERSION}' does not match linked runtime version '$(cat /tmp/mikrozia-version)'" >&2; exit 1; }
RUN test -n "${XRAY_VERSION}" && echo "${XRAY_VERSION}" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || { echo "ERROR: XRAY_VERSION build-arg is required" >&2; exit 1; }
RUN test -n "${MTG_VERSION}" && echo "${MTG_VERSION}" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || { echo "ERROR: MTG_VERSION build-arg is required" >&2; exit 1; }
ENV TZ=Asia/Tehran
ENV HOME=/root
WORKDIR /app

LABEL org.opencontainers.image.title="Mikrozia" \
  org.opencontainers.image.description="3x-ui adapted for MikroTik RouterOS containers" \
  org.opencontainers.image.source="https://github.com/EchoDev7/Mikrozia" \
  org.opencontainers.image.version="${MIKROZIA_VERSION}" \
  io.mikrozia.xray.version="${XRAY_VERSION}" \
  io.mikrozia.mtg.version="${MTG_VERSION}"

RUN apk add --no-cache \
  ca-certificates \
  tzdata \
  bash \
  curl \
  openssl \
  coreutils \
  postgresql18-client=18.6-r0

COPY --from=builder /app/build/ /app/
COPY --from=builder /app/DockerEntrypoint.sh /app/
COPY --from=builder /app/x-ui.sh /usr/bin/x-ui
COPY --from=builder /app/internal/web/translation /app/internal/web/translation
# Records which pinned Geo release each bundled database came from.
COPY --from=builder /app/GEO_DATA_SOURCES /app/GEO_DATA_SOURCES


RUN chmod +x \
  /app/DockerEntrypoint.sh \
  /app/x-ui \
  /usr/bin/x-ui \
  && mkdir -p /etc/x-ui /root/cert /var/log/x-ui

ENV XUI_IN_DOCKER="true"
ENV XUI_MAIN_FOLDER="/app"
ENV XUI_DB_TYPE=""
ENV XUI_DB_DSN=""
EXPOSE 2053 2096
VOLUME [ "/etc/x-ui", "/root/cert" ]
CMD [ "./x-ui" ]
ENTRYPOINT [ "/app/DockerEntrypoint.sh" ]

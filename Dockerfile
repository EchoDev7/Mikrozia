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
RUN go build -ldflags "-w -s" -o build/x-ui main.go
RUN ./DockerInit.sh "$TARGETARCH" "$TARGETVARIANT"

# ========================================================
# Stage: Final Image of 3x-ui
# ========================================================
FROM alpine
ARG MIKROZIA_VERSION=dev
ENV TZ=Asia/Tehran
ENV HOME=/root
WORKDIR /app

LABEL org.opencontainers.image.title="Mikrozia" \
  org.opencontainers.image.description="3x-ui adapted for MikroTik RouterOS containers" \
  org.opencontainers.image.source="https://github.com/EchoDev7/Mikrozia" \
  org.opencontainers.image.version="${MIKROZIA_VERSION}"

RUN apk add --no-cache --update \
  ca-certificates \
  tzdata \
  bash \
  curl \
  openssl \
  coreutils \
  socat

COPY --from=builder /app/build/ /app/
COPY --from=builder /app/DockerEntrypoint.sh /app/
COPY --from=builder /app/x-ui.sh /usr/bin/x-ui
COPY --from=builder /app/internal/web/translation /app/internal/web/translation


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

# Лёгкий образ Go-ядра eopds (сервер + мастер установки).
# Многоступенчатая сборка: Go-бинарник без CGO → минимальный рантайм.

# --- Сборка ---
# Сборщик фиксирован по патч-версии для воспроизводимости.
FROM golang:1.27.4-alpine AS build
WORKDIR /src

# Опционально: короткий git-хэш через --build-arg COMMIT=...
ARG COMMIT=unknown

# Зависимости (кэшируемый слой).
COPY eopds-go/go.mod eopds-go/go.sum ./
RUN go mod download

# Исходники Go-ядра и VERSION для ldflags.
COPY eopds-go/ ./
COPY VERSION /VERSION

RUN VERSION="$(cat /VERSION)" && \
    BUILDTIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" && \
    CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags "-s -w \
    -X github.com/iAHTOH/eopds/eopds-go/internal/version.Version=${VERSION} \
    -X github.com/iAHTOH/eopds/eopds-go/internal/version.Commit=${COMMIT} \
    -X github.com/iAHTOH/eopds/eopds-go/internal/version.BuildTime=${BUILDTIME}" \
    -o /out/eopds ./cmd/eopds

# --- Рантайм ---
# База фиксирована по минору (не latest): 3.20 была EOL, 3.22 — стабильная.
# 3.24 — текущая стабильная ветка с максимальным сроком поддержки вперёд.
FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 1000 eopds \
    && mkdir -p /config /books \
    && chown -R eopds:eopds /config /books

COPY --from=build /out/eopds /usr/local/bin/eopds

ENV EOPDS_CONFIG_DIR=/config \
    EOPDS_ROOT_LIB=/books \
    EOPDS_SERVER_ADDR=:8080 \
    TZ=UTC

USER eopds
WORKDIR /config
EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s \
    CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/usr/local/bin/eopds"]
CMD ["serve"]

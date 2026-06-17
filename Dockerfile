# syntax=docker/dockerfile:1

# ===== build stage =====
FROM golang:1.26-alpine AS build
WORKDIR /src

# 의존성 레이어 캐시 (현재는 stdlib-only 이므로 go.sum 불필요).
COPY go.mod ./
RUN go mod download

# 소스 복사 후 정적 바이너리 빌드 (CGO 비활성).
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway

# ===== runtime stage =====
# distroless: 셸/패키지 없는 최소 런타임 (공격 표면 최소화), 비루트 실행.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /

COPY --from=build /out/gateway /gateway

EXPOSE 8080
USER nonroot:nonroot

# 헬스체크는 바이너리 자체 서브커맨드로 수행 (distroless 에 셸/curl 없음).
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/gateway", "-healthcheck"]

ENTRYPOINT ["/gateway"]

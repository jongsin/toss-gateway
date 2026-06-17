# toss-gateway

토스증권(Toss Securities) Open API 를 위한 **무상태(stateless) · 멀티유저 · Go(표준 라이브러리 only)** API 게이트웨이입니다.

사용자 인증정보를 **저장하지 않고**, 토스의 Rate Limit 을 호출 전에 미리 게이팅하여 한도 초과를 방지합니다. 외부 의존성이 **0개**라서 `go run` 한 줄이면 바로 떠서 누구나 즉시 써볼 수 있습니다.

```
github.com/jongsin/toss-gateway
```

---

## 핵심 특징

| 항목 | 내용 |
|------|------|
| **무상태** | `client_secret` / `access_token` 을 **저장하지 않음**. 토큰 발급은 토스로 프록시해 호출자에게 그대로 반환하고, 이후 호출은 호출자가 토큰을 지참 |
| **멀티유저** | 요청마다 호출자의 `Authorization` 헤더를 그대로 토스로 전달. 서버측 세션/공유 상태 없음 |
| **Rate Limit 사전 게이팅** | `(clientID × API 그룹)` 토큰버킷으로 토스 호출 **전에** 한도를 검사. 위조 토큰의 교차 테넌트 고갈을 환급 로직으로 차단 |
| **제로 의존성** | Go 표준 라이브러리만 사용 (`go.sum` 없음 → 공급망 위험 제거, 에어갭 빌드 가능) |
| **다국어(i18n)** | 게이트웨이 자체 메시지 `ko` / `en` 지원 (`Accept-Language` 헤더 또는 `?lang=`) |
| **자체 문서** | OpenAPI 스펙 + Swagger UI 를 바이너리에 **임베드**(`/docs`, `/openapi.yaml`). 외부 CDN 미참조 |
| **보안 하드닝** | distroless · non-root · read-only FS · `cap_drop ALL` · CSP · 민감정보(토큰/바디) 미로깅 |

---

## 요구사항

둘 중 **하나만** 있으면 됩니다.

- **Go 1.23+** (개발/CI 검증은 Go 1.26 사용) — 소스에서 직접 빌드/실행
- 또는 **Docker** — 컨테이너로 실행

> 외부 패키지를 받지 않으므로(stdlib-only) 인터넷 없이도 빌드됩니다.

---

## 빠른 시작

### A) Go 로 바로 실행 (가장 빠름)

```bash
git clone https://github.com/jongsin/toss-gateway.git
cd toss-gateway

go run ./cmd/gateway          # 기본값(development, :8080)으로 즉시 기동
```

다른 터미널에서 확인:

```bash
curl -s http://localhost:8080/healthz       # {"service":"toss-gateway","status":"ok"}
open  http://localhost:8080/docs            # Swagger UI (macOS; Linux 는 xdg-open)
```

바이너리로 빌드하려면:

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o gateway ./cmd/gateway
./gateway
```

또는 설치형(저장소 공개 시):

```bash
go install github.com/jongsin/toss-gateway/cmd/gateway@latest
gateway          # $GOBIN/gateway 실행
```

### B) Docker 로 실행

```bash
# 이 디렉토리에서 (Dockerfile 포함)
docker build -t toss-gateway .
docker run --rm -p 8080:8080 -e GATEWAY_ENV=development toss-gateway
```

이미지는 distroless · non-root 기반이며, 컨테이너 헬스체크는 셸 없이 바이너리 서브커맨드(`gateway -healthcheck`)로 수행합니다.

> 💡 모노레포 전체(게이트웨이 + 향후 서비스/DB)를 한 번에 띄우려면 상위 디렉토리의 `bootstrap.sh` / `docker-compose.yml` 을 사용하세요. 이 README 는 **게이트웨이 모듈 단독** 사용을 다룹니다.

---

## 설정 (환경변수)

모든 설정은 환경변수로 주입합니다. **미설정 값은 안전한 기본값**을 사용하므로, 아무것도 설정하지 않아도 개발 모드로 바로 뜹니다.

| 변수 | 기본값 | 설명 |
|------|--------|------|
| `GATEWAY_ENV` | `development` | `development` \| `production`. `production` 이면 JSON 구조화 로깅. **CORS `*` 는 `development` 에서만 허용**(그 외 전부 거부, fail-closed) |
| `GATEWAY_ADDR` | `:8080` | 리슨 주소 |
| `GATEWAY_BASE_PATH` | (빈값) | 경로 prefix (예: `/gateway`). 비우면 루트(`/`). 스펙/문서 URL 에 자동 반영 |
| `TOSS_API_BASE_URL` | `https://openapi.tossinvest.com` | 토스 Open API base URL |
| `TOSS_API_TIMEOUT` | `10s` | 업스트림(토스) 호출 타임아웃 |
| `GATEWAY_READ_TIMEOUT` | `15s` | HTTP read 타임아웃 |
| `GATEWAY_WRITE_TIMEOUT` | `30s` | HTTP write 타임아웃 |
| `GATEWAY_IDLE_TIMEOUT` | `60s` | keep-alive idle 타임아웃 |
| `GATEWAY_SHUTDOWN_TIMEOUT` | `10s` | graceful shutdown 대기 |
| `GATEWAY_MAX_BODY_BYTES` | `1048576` | 요청 바디 최대 크기 (1 MiB) |
| `GATEWAY_CORS_ORIGINS` | (빈값) | 콤마 구분 Origin 화이트리스트. 예: `https://fo.example.com,https://bo.example.com`. 비우면 CORS 비활성 |
| `GATEWAY_DEFAULT_LANG` | `ko` | 기본 응답 언어 `ko` \| `en` |
| `RATE_LIMIT_ENABLED` | `true` | 토스로 보내기 전 한도 초과를 사전 차단 |
| `RATE_LIMIT_SAFETY_RATIO` | `1.0` | `0 < r ≤ 1`. `1.0`=문서 한도 그대로, `0.9`=10% 여유 |
| `GATEWAY_TRUST_PROXY` | `false` | LB/프록시 뒤 배치 시 `X-Forwarded-For` 신뢰 |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |

예시 (`.env` 파일 또는 export):

```bash
export GATEWAY_ENV=production
export GATEWAY_CORS_ORIGINS=https://fo.example.com,https://bo.example.com
export GATEWAY_TRUST_PROXY=true
go run ./cmd/gateway
```

> ⚠️ **토스 인증정보는 설정에 넣지 않습니다.** 무상태 설계상 `client_id`/`client_secret`/토큰은 매 요청 시 호출자가 지참하며, 서버는 절대 저장/로깅하지 않습니다.

---

## 엔드포인트 개요 (`/v1`)

| 그룹 | 메서드 · 경로 | 토스 RL 그룹 | 계좌헤더 |
|------|---------------|--------------|:--------:|
| Auth | `POST /v1/auth/token` | AUTH | - |
| Market Data | `GET /v1/orderbook`, `/prices`, `/trades`, `/price-limits` | MARKET_DATA | - |
| Market Data | `GET /v1/candles` | MARKET_DATA_CHART | - |
| Stock Info | `GET /v1/stocks`, `/stocks/{symbol}/warnings` | STOCK | - |
| Market Info | `GET /v1/exchange-rate`, `/market-calendar/KR`, `/market-calendar/US` | MARKET_INFO | - |
| Account | `GET /v1/accounts` | ACCOUNT | - |
| Asset | `GET /v1/holdings` | ASSET | ✅ |
| Order | `POST /v1/orders`, `/orders/{orderId}/modify`, `/orders/{orderId}/cancel` | ORDER | ✅ |
| Order History | `GET /v1/orders`, `/orders/{orderId}` | ORDER_HISTORY | ✅ |
| Order Info | `GET /v1/buying-power`, `/sellable-quantity`, `/commissions` | ORDER_INFO | ✅ |

**인프라 / 문서**: `GET /healthz`, `GET /readyz`, `GET /docs`, `GET /openapi.yaml`

- 계좌헤더(✅)가 필요한 엔드포인트는 `X-Tossinvest-Account: <accountSeq>` 를 함께 전달합니다. `accountSeq` 는 `GET /v1/accounts` 응답에서 얻습니다.
- `GATEWAY_BASE_PATH` 를 설정하면 모든 경로 앞에 prefix 가 붙습니다(예: `/gateway/v1/...`).

전체 스키마는 기동 후 `/docs`(Swagger UI) 또는 `/openapi.yaml` 에서 확인하세요.

---

## 사용 흐름 예시

```bash
BASE=http://localhost:8080

# 1) 토큰 발급 — 게이트웨이는 저장하지 않고 토스 응답을 그대로 반환
TOKEN=$(curl -s -X POST $BASE/v1/auth/token \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"<ID>","client_secret":"<SECRET>"}' | jq -r .access_token)

# 2) 계좌 조회 (accountSeq 획득)
curl -s $BASE/v1/accounts -H "Authorization: Bearer $TOKEN"

# 3) 시세 조회 (계좌헤더 불필요)
curl -s "$BASE/v1/prices?symbols=005930,000660" -H "Authorization: Bearer $TOKEN"

# 4) 주문 (계좌헤더 필요)
curl -s -X POST $BASE/v1/orders \
  -H "Authorization: Bearer $TOKEN" \
  -H "X-Tossinvest-Account: <accountSeq>" \
  -H 'Content-Type: application/json' \
  -d '{"symbol":"005930","side":"BUY","orderType":"LIMIT","quantity":"10","price":"70000"}'
```

> `client_id` / `client_secret` 은 토스증권 Open API 사용 신청을 통해 발급받습니다.

### 다국어(i18n)

게이트웨이 자체 응답 메시지(에러 등)는 `ko`/`en` 을 지원합니다. 토스에서 받은 본문은 변형하지 않습니다.

```bash
curl -s "$BASE/v1/prices?lang=en" -H "Authorization: Bearer $TOKEN"
curl -s "$BASE/v1/prices" -H "Accept-Language: en" -H "Authorization: Bearer $TOKEN"
```

---

## 개발 · 테스트

```bash
gofmt -l .                 # 포맷 점검 (출력 없으면 clean)
go vet ./...               # 정적 분석
go test ./...              # 단위 테스트
go test -race ./...        # 레이스 검출 포함

go run ./cmd/gateway       # 로컬 실행
```

---

## 보안

- **인증정보 비저장/비로깅**: `Config`/`Server`/`Client` 어디에도 secret/token 필드가 없습니다. 접근 로그는 method/path/status/bytes/ip/duration 만 기록합니다.
- **Rate Limit 보호**: 버킷 맵 크기 상한(LRU evict)·JWT/키 길이 상한으로 메모리 고갈을 유계화. 업스트림 401/403 시 토큰을 환급해 위조 토큰의 표적 고갈을 차단(단, 토큰 발급 경로는 환급하지 않아 credential stuffing 방지).
- **CORS fail-closed**: 와일드카드 `*` 는 `GATEWAY_ENV=development` 에서만 동작하고, 그 외 모든 값에서는 거부됩니다. 운영에서는 명시 Origin 만 사용하세요.
- **/docs CSP**: Swagger UI 자산을 동일 출처로 셀프호스팅하고 `script-src 'self'`·`connect-src 'self'` 로 외부 로드/유출 채널을 차단합니다.
- **컨테이너 하드닝**: distroless · non-root · read-only 루트 FS · `no-new-privileges` · `cap_drop ALL`.

> 운영 권고: 게이트웨이를 공개 인터넷에 직접 노출하지 말고 인증 프록시/WAF/네트워크 ACL 뒤에 배치하고, 앞단에서 TLS 를 종단하세요(게이트웨이는 평문 HTTP).

---

## 프로젝트 구조

```
toss-gateway/
├── cmd/gateway/            # 진입점 (graceful shutdown, -healthcheck 서브커맨드)
├── Dockerfile             # distroless 멀티스테이지 빌드
└── internal/
    ├── config/             # 환경변수 설정 로드
    ├── i18n/               # ko/en 메시지
    ├── ratelimit/          # 토큰버킷 리미터 (peak/adaptive/429/환급/evict)
    ├── tossclient/         # 무상태 토스 API 클라이언트 + JWT sub 파싱(RL 키 전용)
    ├── httpapi/            # HTTP 서버 · 미들웨어 · 핸들러
    └── openapi/            # OpenAPI 스펙 + Swagger UI 자산 (go:embed)
```

---

## 라이선스 / 고지

- **본 게이트웨이는 [Apache License 2.0](LICENSE) 으로 배포됩니다.** Copyright © 2026 jongsin.
- 임베드된 Swagger UI 정적 자산(v5.17.14)도 Apache-2.0, © SmartBear Software 입니다. 상세는 [`internal/openapi/assets/LICENSE.md`](internal/openapi/assets/LICENSE.md) 참고.
- 본 게이트웨이는 토스증권 Open API 의 비공식 클라이언트/중계자이며, 토스가 인증·인가의 최종 권위입니다.

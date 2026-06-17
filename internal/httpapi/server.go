// Package httpapi 는 토스증권 Open API 게이트웨이의 HTTP 레이어이다.
//
// 무상태(stateless) 설계:
//   - 어떤 사용자 인증정보(client_secret)·토큰도 저장하지 않는다.
//   - /v1/auth/token 은 토스로 프록시하여 발급된 토큰을 호출자에게 그대로 반환한다.
//   - 그 외 모든 엔드포인트는 호출자가 지참한 Authorization 헤더를 토스로 전달한다.
//   - rate limit 키는 JWT sub(client_id)에서 파생하며, 키 도출 외 용도로 토큰을 사용하지 않는다.
package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jongsin/toss-gateway/internal/config"
	"github.com/jongsin/toss-gateway/internal/openapi"
	"github.com/jongsin/toss-gateway/internal/ratelimit"
	"github.com/jongsin/toss-gateway/internal/tossclient"
)

// Server 는 게이트웨이 HTTP 서버이다. 사용자 인증정보는 보관하지 않는다.
type Server struct {
	cfg     *config.Config
	logger  *slog.Logger
	toss    *tossclient.Client
	limiter *ratelimit.Limiter
	handler http.Handler
	stop    chan struct{} // 백그라운드 작업(rate limiter janitor) 종료 신호
}

// New 는 설정과 로거로 게이트웨이 서버를 구성한다.
func New(cfg *config.Config, logger *slog.Logger) *Server {
	s := &Server{
		cfg:     cfg,
		logger:  logger,
		toss:    tossclient.New(cfg.TossBaseURL, cfg.UpstreamTimeout),
		limiter: ratelimit.New(cfg.RateLimitSafety, cfg.RateLimitEnabled),
		stop:    make(chan struct{}),
	}
	s.handler = s.routes()
	return s
}

// Handler 는 구성된 http.Handler 를 반환한다.
func (s *Server) Handler() http.Handler { return s.handler }

// ServeHTTP 는 Server 가 http.Handler 를 만족하게 한다.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// StartBackground 는 rate limiter 의 미사용 버킷 청소 고루틴을 시작한다.
func (s *Server) StartBackground() {
	s.limiter.StartJanitor(5*time.Minute, 30*time.Minute, s.stop)
}

// Shutdown 은 백그라운드 작업을 정지한다. (HTTP 서버 종료는 호출자가 관리)
func (s *Server) Shutdown() {
	select {
	case <-s.stop:
		// 이미 닫힘
	default:
		close(s.stop)
	}
}

// routes 는 라우팅 테이블과 미들웨어 체인을 구성한다.
//
// Go 1.22+ ServeMux 의 "메서드 + 패턴" 라우팅을 사용한다. base path prefix 를 적용하며,
// 매칭되지 않는 모든 경로(잘못된 메서드 포함)는 catch-all "/" 로 흡수되어 일관된 JSON 404 를 반환한다.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	b := s.cfg.BasePath // "" 또는 "/gateway"

	// ---- 인프라 / 문서 ----
	mux.HandleFunc("GET "+b+"/healthz", s.handleHealth)
	mux.HandleFunc("GET "+b+"/readyz", s.handleReady)
	mux.HandleFunc("GET "+b+"/docs", s.handleDocs)
	mux.HandleFunc("GET "+b+"/docs/swagger-ui.css", s.handleSwaggerCSS)
	mux.HandleFunc("GET "+b+"/docs/swagger-ui-bundle.js", s.handleSwaggerJS)
	mux.HandleFunc("GET "+b+"/docs/swagger-init.js", s.handleSwaggerInitJS)
	mux.HandleFunc("GET "+b+"/openapi.yaml", s.handleSpec)

	// ---- Auth (무상태 토큰 발급 프록시) ----
	mux.HandleFunc("POST "+b+"/v1/auth/token", s.handleIssueToken)

	// ---- Market Data ----
	mux.HandleFunc("GET "+b+"/v1/orderbook", s.handleOrderbook)
	mux.HandleFunc("GET "+b+"/v1/prices", s.handlePrices)
	mux.HandleFunc("GET "+b+"/v1/trades", s.handleTrades)
	mux.HandleFunc("GET "+b+"/v1/price-limits", s.handlePriceLimits)
	mux.HandleFunc("GET "+b+"/v1/candles", s.handleCandles)

	// ---- Stock Info ----
	mux.HandleFunc("GET "+b+"/v1/stocks", s.handleStocks)
	mux.HandleFunc("GET "+b+"/v1/stocks/{symbol}/warnings", s.handleStockWarnings)

	// ---- Market Info ----
	mux.HandleFunc("GET "+b+"/v1/exchange-rate", s.handleExchangeRate)
	mux.HandleFunc("GET "+b+"/v1/market-calendar/KR", s.handleKrCalendar)
	mux.HandleFunc("GET "+b+"/v1/market-calendar/US", s.handleUsCalendar)

	// ---- Account ----
	mux.HandleFunc("GET "+b+"/v1/accounts", s.handleAccounts)

	// ---- Asset ----
	mux.HandleFunc("GET "+b+"/v1/holdings", s.handleHoldings)

	// ---- Order (주문 생성/정정/취소) ----
	mux.HandleFunc("POST "+b+"/v1/orders", s.handleCreateOrder)
	mux.HandleFunc("POST "+b+"/v1/orders/{orderId}/modify", s.handleModifyOrder)
	mux.HandleFunc("POST "+b+"/v1/orders/{orderId}/cancel", s.handleCancelOrder)

	// ---- Order History (조회) ----
	mux.HandleFunc("GET "+b+"/v1/orders", s.handleListOrders)
	mux.HandleFunc("GET "+b+"/v1/orders/{orderId}", s.handleGetOrder)

	// ---- Order Info ----
	mux.HandleFunc("GET "+b+"/v1/buying-power", s.handleBuyingPower)
	mux.HandleFunc("GET "+b+"/v1/sellable-quantity", s.handleSellableQuantity)
	mux.HandleFunc("GET "+b+"/v1/commissions", s.handleCommissions)

	// ---- catch-all: 미매칭 경로/메서드 → JSON 404 ----
	mux.HandleFunc("/", s.handleNotFound)

	// 미들웨어 체인 (바깥→안쪽): 컨텍스트 주입 → 패닉복구 → 접근로그 → CORS → 라우터
	return s.contextMW(s.recoverMW(s.logMW(s.corsMW(mux))))
}

// ---- 인프라 / 문서 핸들러 ----

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "toss-gateway"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	b := s.cfg.BasePath
	specURL := b + "/openapi.yaml"
	cssURL := b + "/docs/swagger-ui.css"
	jsURL := b + "/docs/swagger-ui-bundle.js"
	initURL := b + "/docs/swagger-init.js"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// 셀프 호스팅 자산만 허용하는 CSP(SEC-04 + N-B 보강): 외부 스크립트/스타일 로드와
	// 외부 유출 채널(connect/img/font)을 차단한다. 부트스트랩을 외부 파일
	// (/docs/swagger-init.js)로 분리해 인라인 스크립트를 제거했으므로 script-src 는 'self'
	// 만 허용한다('unsafe-inline' 불요). 스타일은 Swagger UI 가 런타임에 인라인 주입하므로
	// style-src 에만 'unsafe-inline' 을 유지한다(스타일은 스크립트 실행 불가, 위험 경미).
	// 번들의 유일한 new Function 은 webpack globalThis 폴리필(try/catch + window 폴백)이라
	// 'unsafe-eval' 없이 동작한다.
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; "+
			"base-uri 'self'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openapi.SwaggerUI(specURL, cssURL, jsURL, initURL))
}

// handleSwaggerCSS / handleSwaggerJS / handleSwaggerInitJS 는 셀프 호스팅 Swagger UI
// 정적 자산을 서빙한다(SEC-04, N-B).
func (s *Server) handleSwaggerCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openapi.SwaggerCSS)
}

func (s *Server) handleSwaggerJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openapi.SwaggerBundleJS)
}

func (s *Server) handleSwaggerInitJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openapi.SwaggerInitJS)
}

func (s *Server) handleSpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openapi.SpecYAML)
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeGatewayError(w, r, http.StatusNotFound, "not-found", nil)
}

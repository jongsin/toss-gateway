package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jongsin/toss-gateway/internal/tossclient"
)

// proxy 는 인증/계좌헤더/rate limit 게이팅 후 토스 호출을 수행하는 공통 파이프라인이다.
// 모든 프록시 endpoint 는 Authorization 헤더(Bearer 토큰)를 요구한다.
func (s *Server) proxy(w http.ResponseWriter, r *http.Request, group string, needAccount bool, fn func(cred tossclient.Credentials) (*tossclient.Response, error)) {
	auth := r.Header.Get(tossclient.HeaderAuthorization)
	if strings.TrimSpace(auth) == "" {
		writeGatewayError(w, r, http.StatusUnauthorized, "unauthorized", nil)
		return
	}
	cred := tossclient.Credentials{Authorization: auth}

	if needAccount {
		acc := strings.TrimSpace(r.Header.Get(tossclient.HeaderAccount))
		if acc == "" {
			writeGatewayError(w, r, http.StatusBadRequest, "account-header-required", nil)
			return
		}
		if n, err := strconv.ParseInt(acc, 10, 64); err != nil || n <= 0 {
			writeGatewayError(w, r, http.StatusBadRequest, "invalid-account", map[string]any{"field": tossclient.HeaderAccount})
			return
		}
		cred.AccountSeq = acc
	}

	clientID := tossclient.ClientIDFromAuth(auth)
	if clientID == "" {
		clientID = "anon:" + fingerprint(clientIP(r, s.cfg.TrustProxyHeader))
	}
	if ok, retry := s.limiter.Allow(clientID, group); !ok {
		writeRateLimited(w, r, retry)
		return
	}

	resp, err := fn(cred)
	if err != nil {
		s.writeUpstreamErr(w, r, err)
		return
	}
	s.settle(clientID, group, resp)
	writeUpstream(w, r, resp)
}

func (s *Server) observe(clientID, group string, resp *tossclient.Response) {
	var limit float64
	if resp.RateLimit.Present {
		limit = resp.RateLimit.Limit
	}
	var retry time.Duration
	if resp.StatusCode == http.StatusTooManyRequests {
		retry = resp.RateLimit.RetryAfter
	}
	s.limiter.Observe(clientID, group, limit, retry)
}

// settle 은 프록시 endpoint 응답을 정산한다: 헤더 적응(observe) 후, 업스트림이
// 인증 실패(401/403)를 반환했으면 사전 소비한 토큰을 환급한다.
//
// 환급은 위조 JWT sub 로 타인 버킷을 고갈시키는 교차 테넌트 DoS(SEC-01)를 완화한다.
// 정당한 토스 호출(2xx 등)은 환급하지 않으므로 토스 한도 보호는 유지된다.
// AUTH(토큰 발급)에는 적용하지 않는다(자격증명 스터핑 우회 방지) → handleIssueToken 은 observe 만 사용.
func (s *Server) settle(clientID, group string, resp *tossclient.Response) {
	s.observe(clientID, group, resp)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		s.limiter.Refund(clientID, group)
	}
}

func (s *Server) writeUpstreamErr(w http.ResponseWriter, r *http.Request, err error) {
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() || errors.Is(err, context.DeadlineExceeded) {
		writeGatewayError(w, r, http.StatusGatewayTimeout, "upstream-timeout", nil)
		return
	}
	s.logger.Error("upstream call failed",
		slog.String("requestId", requestIDFrom(r)),
		slog.String("error", err.Error()),
	)
	writeGatewayError(w, r, http.StatusBadGateway, "upstream-unavailable", nil)
}

func writeRateLimited(w http.ResponseWriter, r *http.Request, retry time.Duration) {
	secs := max(int(math.Ceil(retry.Seconds())), 1)
	w.Header().Set(tossclient.HeaderRetryAfter, strconv.Itoa(secs))
	writeGatewayError(w, r, http.StatusTooManyRequests, "rate-limit-exceeded", map[string]any{"retryAfterSeconds": secs})
}

func writeMissing(w http.ResponseWriter, r *http.Request, field string) {
	writeGatewayError(w, r, http.StatusBadRequest, "missing-parameter", map[string]any{"field": field})
}

func (s *Server) readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeGatewayError(w, r, http.StatusRequestEntityTooLarge, "payload-too-large", nil)
		return nil, false
	}
	return body, true
}

func q(r *http.Request, key string) string { return strings.TrimSpace(r.URL.Query().Get(key)) }

// parseOptionalInt 는 선택적 정수 쿼리 파라미터를 파싱한다. (present, value, valid)
func parseOptionalInt(r *http.Request, key string) (int, bool, bool) {
	raw := q(r, key)
	if raw == "" {
		return 0, false, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, true, false
	}
	return n, true, true
}

// ===== Auth =====

func (s *Server) handleIssueToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10) // 토큰 요청 바디는 작다.
	clientID, clientSecret, ok := parseTokenCredentials(r)
	if !ok || clientID == "" || clientSecret == "" {
		writeGatewayError(w, r, http.StatusBadRequest, "invalid-request", map[string]any{"field": "client_id,client_secret"})
		return
	}
	if ok, retry := s.limiter.Allow(clientID, "AUTH"); !ok {
		writeRateLimited(w, r, retry)
		return
	}
	resp, err := s.toss.IssueToken(r.Context(), tossclient.TokenRequest{ClientID: clientID, ClientSecret: clientSecret})
	clientSecret = "" // 사용 후 즉시 폐기 (보관하지 않음)
	_ = clientSecret
	if err != nil {
		s.writeUpstreamErr(w, r, err)
		return
	}
	s.observe(clientID, "AUTH", resp)
	writeUpstream(w, r, resp)
}

func parseTokenCredentials(r *http.Request) (id, secret string, ok bool) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		var body struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return "", "", false
		}
		return strings.TrimSpace(body.ClientID), body.ClientSecret, true
	}
	if err := r.ParseForm(); err != nil {
		return "", "", false
	}
	return strings.TrimSpace(r.PostFormValue("client_id")), r.PostFormValue("client_secret"), true
}

// ===== Market Data =====

func (s *Server) handleOrderbook(w http.ResponseWriter, r *http.Request) {
	symbol := q(r, "symbol")
	if symbol == "" {
		writeMissing(w, r, "symbol")
		return
	}
	s.proxy(w, r, "MARKET_DATA", false, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetOrderbook(r.Context(), cred, symbol)
	})
}

func (s *Server) handlePrices(w http.ResponseWriter, r *http.Request) {
	symbols := q(r, "symbols")
	if symbols == "" {
		writeMissing(w, r, "symbols")
		return
	}
	s.proxy(w, r, "MARKET_DATA", false, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetPrices(r.Context(), cred, symbols)
	})
}

func (s *Server) handleTrades(w http.ResponseWriter, r *http.Request) {
	symbol := q(r, "symbol")
	if symbol == "" {
		writeMissing(w, r, "symbol")
		return
	}
	count, _, valid := parseOptionalInt(r, "count")
	if !valid {
		writeGatewayError(w, r, http.StatusBadRequest, "invalid-request", map[string]any{"field": "count"})
		return
	}
	s.proxy(w, r, "MARKET_DATA", false, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetTrades(r.Context(), cred, tossclient.TradesParams{Symbol: symbol, Count: count})
	})
}

func (s *Server) handlePriceLimits(w http.ResponseWriter, r *http.Request) {
	symbol := q(r, "symbol")
	if symbol == "" {
		writeMissing(w, r, "symbol")
		return
	}
	s.proxy(w, r, "MARKET_DATA", false, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetPriceLimits(r.Context(), cred, symbol)
	})
}

func (s *Server) handleCandles(w http.ResponseWriter, r *http.Request) {
	symbol := q(r, "symbol")
	interval := q(r, "interval")
	if symbol == "" {
		writeMissing(w, r, "symbol")
		return
	}
	if interval == "" {
		writeMissing(w, r, "interval")
		return
	}
	if interval != "1m" && interval != "1d" {
		writeGatewayError(w, r, http.StatusBadRequest, "invalid-request", map[string]any{"field": "interval", "allowedValues": []string{"1m", "1d"}})
		return
	}
	count, _, valid := parseOptionalInt(r, "count")
	if !valid {
		writeGatewayError(w, r, http.StatusBadRequest, "invalid-request", map[string]any{"field": "count"})
		return
	}
	p := tossclient.CandlesParams{Symbol: symbol, Interval: interval, Count: count, Before: q(r, "before")}
	if raw := q(r, "adjusted"); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			writeGatewayError(w, r, http.StatusBadRequest, "invalid-request", map[string]any{"field": "adjusted"})
			return
		}
		p.Adjusted = &b
	}
	s.proxy(w, r, "MARKET_DATA_CHART", false, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetCandles(r.Context(), cred, p)
	})
}

// ===== Stock Info =====

func (s *Server) handleStocks(w http.ResponseWriter, r *http.Request) {
	symbols := q(r, "symbols")
	if symbols == "" {
		writeMissing(w, r, "symbols")
		return
	}
	s.proxy(w, r, "STOCK", false, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetStocks(r.Context(), cred, symbols)
	})
}

func (s *Server) handleStockWarnings(w http.ResponseWriter, r *http.Request) {
	symbol := strings.TrimSpace(r.PathValue("symbol"))
	if symbol == "" {
		writeMissing(w, r, "symbol")
		return
	}
	s.proxy(w, r, "STOCK", false, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetStockWarnings(r.Context(), cred, symbol)
	})
}

// ===== Market Info =====

func (s *Server) handleExchangeRate(w http.ResponseWriter, r *http.Request) {
	base := q(r, "baseCurrency")
	quote := q(r, "quoteCurrency")
	if base == "" {
		writeMissing(w, r, "baseCurrency")
		return
	}
	if quote == "" {
		writeMissing(w, r, "quoteCurrency")
		return
	}
	s.proxy(w, r, "MARKET_INFO", false, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetExchangeRate(r.Context(), cred, tossclient.ExchangeRateParams{
			BaseCurrency: base, QuoteCurrency: quote, DateTime: q(r, "dateTime"),
		})
	})
}

func (s *Server) handleKrCalendar(w http.ResponseWriter, r *http.Request) {
	date := q(r, "date")
	s.proxy(w, r, "MARKET_INFO", false, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetKrMarketCalendar(r.Context(), cred, date)
	})
}

func (s *Server) handleUsCalendar(w http.ResponseWriter, r *http.Request) {
	date := q(r, "date")
	s.proxy(w, r, "MARKET_INFO", false, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetUsMarketCalendar(r.Context(), cred, date)
	})
}

// ===== Account =====

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	s.proxy(w, r, "ACCOUNT", false, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetAccounts(r.Context(), cred)
	})
}

// ===== Asset =====

func (s *Server) handleHoldings(w http.ResponseWriter, r *http.Request) {
	symbol := q(r, "symbol")
	s.proxy(w, r, "ASSET", true, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetHoldings(r.Context(), cred, symbol)
	})
}

// ===== Order =====

func (s *Server) handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readBody(w, r)
	if !ok {
		return
	}
	if code, field, bad := validateOrderCreate(body); bad {
		writeGatewayError(w, r, http.StatusBadRequest, code, map[string]any{"field": field})
		return
	}
	s.proxy(w, r, "ORDER", true, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.CreateOrder(r.Context(), cred, body)
	})
}

func (s *Server) handleModifyOrder(w http.ResponseWriter, r *http.Request) {
	orderID := strings.TrimSpace(r.PathValue("orderId"))
	if orderID == "" {
		writeMissing(w, r, "orderId")
		return
	}
	body, ok := s.readBody(w, r)
	if !ok {
		return
	}
	if code, field, bad := validateOrderModify(body); bad {
		writeGatewayError(w, r, http.StatusBadRequest, code, map[string]any{"field": field})
		return
	}
	s.proxy(w, r, "ORDER", true, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.ModifyOrder(r.Context(), cred, orderID, body)
	})
}

func (s *Server) handleCancelOrder(w http.ResponseWriter, r *http.Request) {
	orderID := strings.TrimSpace(r.PathValue("orderId"))
	if orderID == "" {
		writeMissing(w, r, "orderId")
		return
	}
	s.proxy(w, r, "ORDER", true, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.CancelOrder(r.Context(), cred, orderID)
	})
}

// ===== Order History =====

func (s *Server) handleListOrders(w http.ResponseWriter, r *http.Request) {
	status := q(r, "status")
	if status == "" {
		writeMissing(w, r, "status")
		return
	}
	if status != "OPEN" && status != "CLOSED" {
		writeGatewayError(w, r, http.StatusBadRequest, "invalid-request", map[string]any{"field": "status", "allowedValues": []string{"OPEN", "CLOSED"}})
		return
	}
	limit, _, valid := parseOptionalInt(r, "limit")
	if !valid {
		writeGatewayError(w, r, http.StatusBadRequest, "invalid-request", map[string]any{"field": "limit"})
		return
	}
	p := tossclient.OrdersParams{
		Status: status, Symbol: q(r, "symbol"), From: q(r, "from"),
		To: q(r, "to"), Cursor: q(r, "cursor"), Limit: limit,
	}
	s.proxy(w, r, "ORDER_HISTORY", true, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetOrders(r.Context(), cred, p)
	})
}

func (s *Server) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	orderID := strings.TrimSpace(r.PathValue("orderId"))
	if orderID == "" {
		writeMissing(w, r, "orderId")
		return
	}
	s.proxy(w, r, "ORDER_HISTORY", true, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetOrder(r.Context(), cred, orderID)
	})
}

// ===== Order Info =====

func (s *Server) handleBuyingPower(w http.ResponseWriter, r *http.Request) {
	currency := q(r, "currency")
	if currency == "" {
		writeMissing(w, r, "currency")
		return
	}
	s.proxy(w, r, "ORDER_INFO", true, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetBuyingPower(r.Context(), cred, currency)
	})
}

func (s *Server) handleSellableQuantity(w http.ResponseWriter, r *http.Request) {
	symbol := q(r, "symbol")
	if symbol == "" {
		writeMissing(w, r, "symbol")
		return
	}
	s.proxy(w, r, "ORDER_INFO", true, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetSellableQuantity(r.Context(), cred, symbol)
	})
}

func (s *Server) handleCommissions(w http.ResponseWriter, r *http.Request) {
	s.proxy(w, r, "ORDER_INFO", true, func(cred tossclient.Credentials) (*tossclient.Response, error) {
		return s.toss.GetCommissions(r.Context(), cred)
	})
}

// ===== 검증 =====

// validateOrderCreate 는 주문 생성 요청의 기본 유효성을 검증한다.
// 깊은 규칙(호가단위/고액주문/거래시간 등)은 토스가 권위 있게 처리한다.
func validateOrderCreate(body []byte) (code, field string, bad bool) {
	var o tossclient.OrderCreateRequest
	if err := json.Unmarshal(body, &o); err != nil {
		return "invalid-request", "", true
	}
	if strings.TrimSpace(o.Symbol) == "" {
		return "invalid-request", "symbol", true
	}
	if o.Side != "BUY" && o.Side != "SELL" {
		return "invalid-request", "side", true
	}
	if o.OrderType != "LIMIT" && o.OrderType != "MARKET" {
		return "invalid-request", "orderType", true
	}
	hasQty := strings.TrimSpace(o.Quantity) != ""
	hasAmt := strings.TrimSpace(o.OrderAmount) != ""
	if hasQty == hasAmt { // 둘 다 있거나 둘 다 없음 → 오류
		return "invalid-request", "quantity,orderAmount", true
	}
	if o.OrderType == "LIMIT" && strings.TrimSpace(o.Price) == "" {
		return "invalid-request", "price", true
	}
	return "", "", false
}

func validateOrderModify(body []byte) (code, field string, bad bool) {
	var o tossclient.OrderModifyRequest
	if err := json.Unmarshal(body, &o); err != nil {
		return "invalid-request", "", true
	}
	if o.OrderType != "LIMIT" && o.OrderType != "MARKET" {
		return "invalid-request", "orderType", true
	}
	if o.OrderType == "LIMIT" && strings.TrimSpace(o.Price) == "" {
		return "invalid-request", "price", true
	}
	return "", "", false
}

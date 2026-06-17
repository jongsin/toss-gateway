package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jongsin/toss-gateway/internal/config"
)

func newTestServer(tossURL string, rateLimit bool) *Server {
	cfg := config.Load()
	cfg.TossBaseURL = tossURL
	cfg.RateLimitEnabled = rateLimit
	cfg.CORSOrigins = []string{"https://app.example.com"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, logger)
}

func decodeEnvelope(t *testing.T, body []byte) errorEnvelope {
	t.Helper()
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("에러 응답 디코딩 실패: %v (body=%s)", err, body)
	}
	return env
}

func TestRouting_NotFoundJSON(t *testing.T) {
	s := newTestServer("http://upstream.invalid", false)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no/such/route", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
	env := decodeEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != "not-found" {
		t.Fatalf("code=%s, want not-found", env.Error.Code)
	}
	if env.Error.RequestID == "" {
		t.Fatal("requestId 가 비어있다")
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Fatal("X-Request-Id 헤더가 없다")
	}
}

func TestHealthz(t *testing.T) {
	s := newTestServer("http://upstream.invalid", false)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestDocsAndSpec(t *testing.T) {
	s := newTestServer("http://upstream.invalid", false)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "swagger-ui") {
		t.Fatalf("docs status=%d body0=%.40s", rec.Code, rec.Body.String())
	}
	// spec URL 이 주입되었는지
	if !strings.Contains(rec.Body.String(), "/openapi.yaml") {
		t.Fatal("docs 에 spec URL 이 주입되지 않았다")
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "openapi:") {
		t.Fatalf("spec status=%d", rec.Code)
	}
}

func TestProxy_RequiresAuth(t *testing.T) {
	s := newTestServer("http://upstream.invalid", false)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commissions", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", rec.Code)
	}
	if env := decodeEnvelope(t, rec.Body.Bytes()); env.Error.Code != "unauthorized" {
		t.Fatalf("code=%s, want unauthorized", env.Error.Code)
	}
}

func TestProxy_RequiresAccountHeader(t *testing.T) {
	s := newTestServer("http://upstream.invalid", false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/commissions", nil)
	req.Header.Set("Authorization", "Bearer x")
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
	if env := decodeEnvelope(t, rec.Body.Bytes()); env.Error.Code != "account-header-required" {
		t.Fatalf("code=%s, want account-header-required", env.Error.Code)
	}
}

func TestProxy_InvalidAccountHeader(t *testing.T) {
	s := newTestServer("http://upstream.invalid", false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/commissions", nil)
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("X-Tossinvest-Account", "abc") // 양의 정수가 아님
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
	if env := decodeEnvelope(t, rec.Body.Bytes()); env.Error.Code != "invalid-account" {
		t.Fatalf("code=%s, want invalid-account", env.Error.Code)
	}
}

func TestMissingQueryParam(t *testing.T) {
	s := newTestServer("http://upstream.invalid", false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/orderbook", nil) // symbol 누락
	req.Header.Set("Authorization", "Bearer x")
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
	env := decodeEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != "missing-parameter" {
		t.Fatalf("code=%s, want missing-parameter", env.Error.Code)
	}
}

func TestI18n_EnglishViaAcceptLanguage(t *testing.T) {
	s := newTestServer("http://upstream.invalid", false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/commissions", nil) // 인증 누락 → unauthorized
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	s.ServeHTTP(rec, req)
	env := decodeEnvelope(t, rec.Body.Bytes())
	if !strings.Contains(env.Error.Message, "Authentication") {
		t.Fatalf("영어 메시지가 아니다: %q", env.Error.Message)
	}
}

func TestI18n_KoreanViaQueryParam(t *testing.T) {
	s := newTestServer("http://upstream.invalid", false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/commissions?lang=ko", nil)
	s.ServeHTTP(rec, req)
	env := decodeEnvelope(t, rec.Body.Bytes())
	if !strings.Contains(env.Error.Message, "인증") {
		t.Fatalf("한국어 메시지가 아니다: %q", env.Error.Message)
	}
}

// 무상태 토큰 발급: 게이트웨이는 토큰을 저장하지 않고 호출자에게 그대로 반환한다.
func TestIssueToken_StatelessProxy(t *testing.T) {
	var gotGrant string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotGrant = r.PostFormValue("grant_type")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"ey.tok","token_type":"Bearer","expires_in":86400}`))
	}))
	defer upstream.Close()

	s := newTestServer(upstream.URL, false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/token", strings.NewReader(`{"client_id":"id","client_secret":"sec"}`))
	req.Header.Set("Content-Type", "application/json")
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if gotGrant != "client_credentials" {
		t.Fatalf("grant_type=%s", gotGrant)
	}
	if !strings.Contains(rec.Body.String(), "ey.tok") {
		t.Fatalf("토큰이 호출자에게 반환되지 않았다: %s", rec.Body.String())
	}
}

func TestIssueToken_MissingCredentials(t *testing.T) {
	s := newTestServer("http://upstream.invalid", false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/token", strings.NewReader(`{"client_id":"id"}`))
	req.Header.Set("Content-Type", "application/json")
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}

func TestCreateOrder_ValidationRejectsAmbiguousQuantity(t *testing.T) {
	s := newTestServer("http://upstream.invalid", false)
	// 수량/금액 둘 다 없음 → 검증 실패
	body := `{"symbol":"005930","side":"BUY","orderType":"LIMIT","price":"70000"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/orders", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("X-Tossinvest-Account", "1")
	req.Header.Set("Content-Type", "application/json")
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
	if env := decodeEnvelope(t, rec.Body.Bytes()); env.Error.Code != "invalid-request" {
		t.Fatalf("code=%s, want invalid-request", env.Error.Code)
	}
}

// rate limit 게이팅: 동일 클라이언트(동일 IP anon 키)가 ACCOUNT(1 TPS)를 즉시 2회 → 2회차 429.
func TestRateLimit_Enforced(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer upstream.Close()

	s := newTestServer(upstream.URL, true)
	call := func() int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/accounts", nil)
		req.Header.Set("Authorization", "Bearer x")
		s.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := call(); c != http.StatusOK {
		t.Fatalf("첫 호출=%d, want 200", c)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts", nil)
	req.Header.Set("Authorization", "Bearer x")
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("두 번째 호출=%d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 응답에 Retry-After 헤더가 필요하다")
	}
}

func TestCORS_PreflightAllowed(t *testing.T) {
	s := newTestServer("http://upstream.invalid", false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/v1/accounts", nil)
	req.Header.Set("Origin", "https://app.example.com")
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status=%d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("CORS origin 헤더 누락: %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORS_DisallowedOrigin(t *testing.T) {
	s := newTestServer("http://upstream.invalid", false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/v1/accounts", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	s.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("허용되지 않은 Origin 에 CORS 헤더가 노출되었다")
	}
}

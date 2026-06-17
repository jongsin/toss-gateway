package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jongsin/toss-gateway/internal/config"
)

// newServerWithEnv 는 특정 환경/CORS 설정으로 테스트 서버를 만든다(CORS 프로덕션 테스트용).
func newServerWithEnv(tossURL, env string, cors []string) *Server {
	cfg := config.Load()
	cfg.TossBaseURL = tossURL
	cfg.Env = env
	cfg.CORSOrigins = cors
	return New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// SEC-01: 업스트림이 401(인증 실패)을 반환하면 게이트웨이가 RL 토큰을 환급한다.
// 따라서 위조/만료 토큰 요청이 (저한도) 버킷을 고갈시키지 못한다.
// 대조: TestRateLimit_Enforced 는 업스트림 200 시 2회차가 429 임을 보인다.
func TestRefund_Upstream401KeepsBucketAvailable(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
	}))
	defer upstream.Close()

	s := newTestServer(upstream.URL, true) // rate limit 활성, ACCOUNT=1 TPS
	call := func() int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/accounts", nil)
		req.Header.Set("Authorization", "Bearer x") // JWT 아님 → anon 키 (동일 IP 공유)
		s.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := call(); c != http.StatusUnauthorized {
		t.Fatalf("첫 호출=%d, want 401(업스트림 통과)", c)
	}
	if c := call(); c != http.StatusUnauthorized {
		t.Fatalf("두 번째 호출=%d, want 401. 429 면 401 환급이 작동하지 않은 것", c)
	}
}

// AUTH(토큰 발급)는 401 환급을 적용하지 않는다(자격증명 스터핑 우회 방지).
// 업스트림 401 을 두 번 시도하면 두 번째는 게이트웨이 429 로 게이팅되어야 한다(AUTH=5 TPS 이므로
// 6회로 확인).
func TestAuth_NoRefundOnUpstream401(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer upstream.Close()

	s := newTestServer(upstream.URL, true) // AUTH = 5 TPS
	call := func() int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/token",
			strings.NewReader(`{"client_id":"victim","client_secret":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")
		s.ServeHTTP(rec, req)
		return rec.Code
	}
	got429 := false
	for i := range 7 { // 5 TPS 한도 초과까지 시도
		if call() == http.StatusTooManyRequests {
			got429 = true
			break
		}
		_ = i
	}
	if !got429 {
		t.Fatal("AUTH 는 401 환급 없이 한도(5 TPS)에서 게이팅되어야 한다(환급 미적용 확인)")
	}
}

// SEC-03: 프로덕션에서는 CORS 와일드카드(*)를 무시하고 임의 Origin 을 반사하지 않는다.
func TestCORS_WildcardIgnoredInProduction(t *testing.T) {
	s := newServerWithEnv("http://upstream.invalid", "production", []string{"*"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/v1/accounts", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	s.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("프로덕션에서 와일드카드 Origin 이 반사되었다: %q", got)
	}
}

// 비프로덕션(development)에서는 와일드카드가 종전대로 동작한다(운영 편의 유지).
func TestCORS_WildcardReflectsInDevelopment(t *testing.T) {
	s := newServerWithEnv("http://upstream.invalid", "development", []string{"*"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/v1/accounts", nil)
	req.Header.Set("Origin", "https://any.example.com")
	s.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://any.example.com" {
		t.Fatalf("개발 환경 와일드카드 반사 실패: %q", got)
	}
}

// N-A(재감사): production 약칭/비-development 환경값에서도 와일드카드가 거부되어야 한다.
// fail-closed 게이트("development 가 아니면 거부")가 prod/live/staging/멀티리전/미지정
// 모두에서 침묵 실패 없이 발동하는지 검증한다.
func TestCORS_WildcardIgnoredForNonDevEnvAliases(t *testing.T) {
	for _, env := range []string{"prod", "production", "live", "staging", "production-eu", "PROD", ""} {
		s := newServerWithEnv("http://upstream.invalid", env, []string{"*"})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodOptions, "/v1/accounts", nil)
		req.Header.Set("Origin", "https://evil.example.com")
		s.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("GATEWAY_ENV=%q 에서 와일드카드 Origin 이 반사되었다(약칭 우회): %q", env, got)
		}
	}
}

// SEC-04: /docs 는 외부 CDN 을 참조하지 않고 셀프 호스팅 자산 경로 + CSP 를 사용하며,
// 자산이 동일 출처에서 200 으로 서빙된다.
func TestSwaggerAssets_SelfHostedWithCSP(t *testing.T) {
	s := newTestServer("http://upstream.invalid", false)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	body := rec.Body.String()
	for _, host := range []string{"cdn.jsdelivr.net", "unpkg.com", "cdnjs.cloudflare.com", "https://", "http://"} {
		if strings.Contains(body, host) {
			t.Fatalf("/docs 가 외부 리소스(%s)를 참조한다", host)
		}
	}
	if !strings.Contains(body, "/docs/swagger-ui.css") || !strings.Contains(body, "/docs/swagger-ui-bundle.js") || !strings.Contains(body, "/docs/swagger-init.js") {
		t.Fatal("/docs 가 셀프 호스팅 자산 경로(css/bundle/init)를 주입하지 않았다")
	}
	// N-B: 부트스트랩이 외부 스크립트로 분리되어 인라인 코드가 페이지에 남아있지 않아야 한다.
	if strings.Contains(body, "SwaggerUIBundle(") {
		t.Fatal("/docs 에 인라인 부트스트랩이 남아있다(외부 swagger-init.js 로 분리되어야 함)")
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "connect-src 'self'") {
		t.Fatalf("CSP 헤더 누락/부적절: %q", csp)
	}
	// N-B: script-src 는 'self' 만 허용하고 'unsafe-inline' 을 포함하지 않아야 한다.
	if !strings.Contains(csp, "script-src 'self';") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("CSP script-src 가 'self' 로 좁혀지지 않았다(unsafe-inline 잔존): %q", csp)
	}

	for _, tc := range []struct{ path, ctype string }{
		{"/docs/swagger-ui.css", "text/css"},
		{"/docs/swagger-ui-bundle.js", "text/javascript"},
		{"/docs/swagger-init.js", "text/javascript"},
	} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d, want 200", tc.path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s 본문이 비어있다", tc.path)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, tc.ctype) {
			t.Fatalf("%s Content-Type=%q, want %s", tc.path, ct, tc.ctype)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s nosniff 헤더 누락", tc.path)
		}
	}
}

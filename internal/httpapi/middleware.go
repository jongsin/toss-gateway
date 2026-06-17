package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jongsin/toss-gateway/internal/i18n"
)

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyLang
)

func requestIDFrom(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKeyRequestID).(string); ok {
		return v
	}
	return ""
}

func langFrom(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKeyLang).(string); ok {
		return v
	}
	return i18n.LangKO
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req-fallback"
	}
	return hex.EncodeToString(b[:])
}

// sanitizeRequestID 는 클라이언트가 보낸 X-Request-Id 를 안전하게 정제한다(로그 인젝션 방지).
func sanitizeRequestID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 64 {
		return ""
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return ""
		}
	}
	return s
}

// contextMW 는 요청 ID 와 언어를 컨텍스트에 주입한다.
func (s *Server) contextMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := sanitizeRequestID(r.Header.Get("X-Request-Id"))
		if rid == "" {
			rid = newRequestID()
		}
		lang := i18n.Resolve(r.URL.Query().Get("lang"), r.Header.Get("Accept-Language"), s.cfg.DefaultLang)
		ctx := context.WithValue(r.Context(), ctxKeyRequestID, rid)
		ctx = context.WithValue(ctx, ctxKeyLang, lang)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// statusRecorder 는 상태코드/바이트 수를 캡처한다(로깅용).
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if sr.status == 0 {
		sr.status = http.StatusOK
	}
	n, err := sr.ResponseWriter.Write(b)
	sr.bytes += n
	return n, err
}

// logMW 는 접근 로그를 남긴다. 민감 정보(Authorization/바디)는 절대 로깅하지 않는다.
func (s *Server) logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(sr, r)
		s.logger.Info("request",
			slog.String("requestId", requestIDFrom(r)),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", sr.status),
			slog.Int("bytes", sr.bytes),
			slog.Duration("duration", time.Since(start)),
			slog.String("ip", clientIP(r, s.cfg.TrustProxyHeader)),
		)
	})
}

// recoverMW 는 패닉을 복구하여 500 을 응답한다(내부 정보 비노출).
func (s *Server) recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("panic recovered",
					slog.String("requestId", requestIDFrom(r)),
					slog.Any("panic", rec),
				)
				writeGatewayError(w, r, http.StatusInternalServerError, "internal-error", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// corsMW 는 화이트리스트 기반 CORS 를 처리한다.
//
// 와일드카드(`*`) Origin 은 명시적으로 development 환경일 때만 허용한다(SEC-03, N-A 보강).
// fail-closed(default-deny): GATEWAY_ENV 가 "development" 가 아닌 모든 값
// (production·prod·live·staging·production-eu·미지정 등)에서는 `*` 를 거부한다.
// production 정확 철자만 막던 종전 게이트는 prod/live 같은 흔한 약칭에서 침묵 실패했으므로,
// "비-development 면 거부"하는 denylist 방식으로 반전하여 약칭 우회를 제거한다.
// 오설정으로 임의 사이트에서 인증 포함 요청이 가능해지는 것을 코드 레벨에서 차단하고,
// 기동 시 경고 로그를 남겨 명시적 Origin 설정을 유도한다.
func (s *Server) corsMW(next http.Handler) http.Handler {
	allowed := make(map[string]bool, len(s.cfg.CORSOrigins))
	for _, o := range s.cfg.CORSOrigins {
		allowed[o] = true
	}
	if allowed["*"] && !strings.EqualFold(strings.TrimSpace(s.cfg.Env), "development") {
		delete(allowed, "*")
		s.logger.Warn("CORS wildcard '*' is ignored outside development; set GATEWAY_CORS_ORIGINS to explicit origins")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (allowed["*"] || allowed[origin]) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Tossinvest-Account, Accept-Language")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func fingerprint(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

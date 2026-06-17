// Package config 는 환경변수(.env) 기반 게이트웨이 설정을 로드한다.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 는 게이트웨이 런타임 설정이다. 어떤 사용자 인증정보도 보관하지 않는다.
type Config struct {
	Env              string        // development | production
	Addr             string        // 리슨 주소 (예: ":8080")
	BasePath         string        // 게이트웨이 경로 prefix (예: "/gateway")
	TossBaseURL      string        // 토스 Open API base URL
	ReadTimeout      time.Duration // HTTP 서버 read 타임아웃
	WriteTimeout     time.Duration // HTTP 서버 write 타임아웃
	IdleTimeout      time.Duration // HTTP keep-alive idle 타임아웃
	UpstreamTimeout  time.Duration // 토스 API 호출 타임아웃
	ShutdownTimeout  time.Duration // graceful shutdown 대기
	MaxBodyBytes     int64         // 요청 바디 최대 크기 (bytes)
	CORSOrigins      []string      // 허용 Origin 화이트리스트 (빈 값=CORS 비활성)
	DefaultLang      string        // 기본 언어 (ko|en)
	RateLimitEnabled bool          // 사전 rate limit 게이팅 활성화
	RateLimitSafety  float64       // 한도 안전계수 (0<r<=1, 1=문서 한도 그대로)
	TrustProxyHeader bool          // X-Forwarded-For 신뢰 여부 (프록시 뒤 배치 시)
	LogLevel         string        // info | debug | warn | error
}

// Load 는 환경변수에서 설정을 읽어 Config 를 생성한다. 미설정 값은 안전한 기본값을 사용한다.
func Load() *Config {
	return &Config{
		Env:              getenv("GATEWAY_ENV", "development"),
		Addr:             getenv("GATEWAY_ADDR", ":8080"),
		BasePath:         strings.TrimRight(getenv("GATEWAY_BASE_PATH", ""), "/"),
		TossBaseURL:      strings.TrimRight(getenv("TOSS_API_BASE_URL", "https://openapi.tossinvest.com"), "/"),
		ReadTimeout:      getdur("GATEWAY_READ_TIMEOUT", 15*time.Second),
		WriteTimeout:     getdur("GATEWAY_WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:      getdur("GATEWAY_IDLE_TIMEOUT", 60*time.Second),
		UpstreamTimeout:  getdur("TOSS_API_TIMEOUT", 10*time.Second),
		ShutdownTimeout:  getdur("GATEWAY_SHUTDOWN_TIMEOUT", 10*time.Second),
		MaxBodyBytes:     getint64("GATEWAY_MAX_BODY_BYTES", 1<<20), // 1 MiB
		CORSOrigins:      splitComma(getenv("GATEWAY_CORS_ORIGINS", "")),
		DefaultLang:      getenv("GATEWAY_DEFAULT_LANG", "ko"),
		RateLimitEnabled: getbool("RATE_LIMIT_ENABLED", true),
		RateLimitSafety:  getfloat("RATE_LIMIT_SAFETY_RATIO", 1.0),
		TrustProxyHeader: getbool("GATEWAY_TRUST_PROXY", false),
		LogLevel:         getenv("LOG_LEVEL", "info"),
	}
}

// getenv 는 환경변수를 읽되 앞뒤 공백을 제거한다. (.env 의 흔한 공백 실수가 라우팅 등을 깨뜨리지 않도록)
func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return def
}

func getdur(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
			return d
		}
	}
	return def
}

func getint64(key string, def int64) int64 {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return n
		}
	}
	return def
}

func getbool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return def
}

func getfloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f > 0 && f <= 1 {
			return f
		}
	}
	return def
}

func splitComma(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/jongsin/toss-gateway/internal/i18n"
	"github.com/jongsin/toss-gateway/internal/tossclient"
)

// errorEnvelope 는 토스 에러 포맷과 동일한 게이트웨이 자체 에러 응답이다.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	RequestID string `json:"requestId"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Data      any    `json:"data,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeGatewayError 는 게이트웨이가 생성한 에러를 i18n 메시지와 함께 토스 포맷으로 응답한다.
func writeGatewayError(w http.ResponseWriter, r *http.Request, status int, code string, data any) {
	rid := requestIDFrom(r)
	w.Header().Set(tossclient.HeaderRequestID, rid)
	msg := i18n.Translate(langFrom(r), code, "")
	writeJSON(w, status, errorEnvelope{Error: errorBody{
		RequestID: rid,
		Code:      code,
		Message:   msg,
		Data:      data,
	}})
}

// writeUpstream 은 토스 응답을 무손실로 전달하고 rate limit 헤더를 함께 전달한다.
func writeUpstream(w http.ResponseWriter, r *http.Request, resp *tossclient.Response) {
	copyHeader(w, resp.Header, tossclient.HeaderRateLimit)
	copyHeader(w, resp.Header, tossclient.HeaderRateRemaining)
	copyHeader(w, resp.Header, tossclient.HeaderRateReset)
	copyHeader(w, resp.Header, tossclient.HeaderRetryAfter)

	rid := resp.RequestID
	if rid == "" {
		rid = requestIDFrom(r)
	}
	w.Header().Set(tossclient.HeaderRequestID, rid)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(resp.StatusCode)
	if len(resp.Body) > 0 {
		_, _ = w.Write(resp.Body)
	}
}

func copyHeader(w http.ResponseWriter, src http.Header, key string) {
	if v := src.Get(key); v != "" {
		w.Header().Set(key, v)
	}
}

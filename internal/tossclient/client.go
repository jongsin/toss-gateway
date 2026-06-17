// Package tossclient 는 토스증권 Open API 를 호출하는 무상태(stateless) 클라이언트 라이브러리이다.
//
// 설계 원칙:
//   - 어떤 인증정보(client_secret)·토큰도 저장하지 않는다. 호출자가 Credentials 로 매 호출 시 지참한다.
//   - 응답 본문은 json.RawMessage 로 무손실 전달하여 상위 스키마 변경에도 강건하다.
//   - rate limit 응답 헤더를 파싱해 호출자(게이트웨이 리미터)가 적응할 수 있게 한다.
package tossclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// 토스 API 표준 헤더.
const (
	HeaderAuthorization = "Authorization"
	HeaderAccount       = "X-Tossinvest-Account"
	HeaderRequestID     = "X-Request-Id"
	HeaderRateLimit     = "X-RateLimit-Limit"
	HeaderRateRemaining = "X-RateLimit-Remaining"
	HeaderRateReset     = "X-RateLimit-Reset"
	HeaderRetryAfter    = "Retry-After"
)

const maxResponseBytes = 8 << 20 // 응답 본문 상한 8MiB (방어적)

// Client 는 토스 Open API 클라이언트이다.
type Client struct {
	baseURL string
	httpc   *http.Client
}

// New 는 클라이언트를 생성한다.
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpc: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// 인증 헤더 유출 방지: 과도한 리다이렉트 차단.
				if len(via) >= 3 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
	}
}

// Credentials 는 호출자가 지참하는 인증정보이다. 게이트웨이는 이를 보관하지 않는다.
type Credentials struct {
	Authorization string // 원본 Authorization 헤더 값 (예: "Bearer eyJ...")
	AccountSeq    string // 선택. X-Tossinvest-Account 헤더 값
}

// RateLimitInfo 는 응답 헤더에서 파싱한 rate limit 메타데이터이다.
type RateLimitInfo struct {
	Present    bool
	Limit      float64
	Remaining  float64
	Reset      float64
	RetryAfter time.Duration
}

// Response 는 토스 API 호출 결과이다.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       json.RawMessage // 원본 JSON (무손실)
	RateLimit  RateLimitInfo
	RequestID  string
}

// IsError 는 응답이 4xx/5xx 인지 여부.
func (r *Response) IsError() bool { return r.StatusCode >= 400 }

// do 는 단일 HTTP 요청을 수행한다.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, cred Credentials, body io.Reader, contentType string) (*Response, error) {
	u := c.baseURL + path
	if enc := query.Encode(); enc != "" {
		u += "?" + enc
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	if cred.Authorization != "" {
		req.Header.Set(HeaderAuthorization, cred.Authorization)
	}
	if cred.AccountSeq != "" {
		req.Header.Set(HeaderAccount, cred.AccountSeq)
	}

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	return &Response{
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       json.RawMessage(raw),
		RequestID:  resp.Header.Get(HeaderRequestID),
		RateLimit:  parseRateLimit(resp.Header),
	}, nil
}

func (c *Client) get(ctx context.Context, cred Credentials, path string, query url.Values) (*Response, error) {
	return c.do(ctx, http.MethodGet, path, query, cred, nil, "")
}

func (c *Client) postJSON(ctx context.Context, cred Credentials, path string, body []byte) (*Response, error) {
	return c.do(ctx, http.MethodPost, path, nil, cred, bytes.NewReader(body), "application/json")
}

func parseRateLimit(h http.Header) RateLimitInfo {
	var rl RateLimitInfo
	if v := h.Get(HeaderRateLimit); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			rl.Limit, rl.Present = f, true
		}
	}
	if v := h.Get(HeaderRateRemaining); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			rl.Remaining, rl.Present = f, true
		}
	}
	if v := h.Get(HeaderRateReset); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			rl.Reset, rl.Present = f, true
		}
	}
	if v := h.Get(HeaderRetryAfter); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			rl.RetryAfter = time.Duration(secs) * time.Second
		}
	}
	return rl
}

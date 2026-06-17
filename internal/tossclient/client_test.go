package tossclient

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIssueToken_PostsClientCredentialsForm(t *testing.T) {
	var gotPath, gotCT, gotGrant, gotID, gotSecret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		gotGrant = r.PostFormValue("grant_type")
		gotID = r.PostFormValue("client_id")
		gotSecret = r.PostFormValue("client_secret")
		w.Header().Set("X-Request-Id", "req-123")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, 5*time.Second)
	resp, err := c.IssueToken(context.Background(), TokenRequest{ClientID: "id-1", ClientSecret: "sec-1"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/oauth2/token" {
		t.Fatalf("path=%s, want /oauth2/token", gotPath)
	}
	if !strings.HasPrefix(gotCT, "application/x-www-form-urlencoded") {
		t.Fatalf("content-type=%s", gotCT)
	}
	if gotGrant != "client_credentials" {
		t.Fatalf("grant_type=%s", gotGrant)
	}
	if gotID != "id-1" || gotSecret != "sec-1" {
		t.Fatalf("creds id=%s secret=%s", gotID, gotSecret)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if resp.RequestID != "req-123" {
		t.Fatalf("requestId=%s", resp.RequestID)
	}
}

// 무상태성: 클라이언트는 호출자가 지참한 Authorization/계좌 헤더를 그대로 전달한다.
func TestGet_ForwardsCredentialHeaders(t *testing.T) {
	var auth, acc string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		acc = r.Header.Get("X-Tossinvest-Account")
		w.Header().Set("X-RateLimit-Limit", "10")
		w.Header().Set("X-RateLimit-Remaining", "9")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL, 5*time.Second)
	cred := Credentials{Authorization: "Bearer abc", AccountSeq: "42"}
	resp, err := c.GetHoldings(context.Background(), cred, "")
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer abc" {
		t.Fatalf("authorization=%s", auth)
	}
	if acc != "42" {
		t.Fatalf("account=%s", acc)
	}
	if !resp.RateLimit.Present || resp.RateLimit.Limit != 10 || resp.RateLimit.Remaining != 9 {
		t.Fatalf("ratelimit=%+v", resp.RateLimit)
	}
}

func TestGetAccounts_NoAccountHeaderSent(t *testing.T) {
	var hasAcc bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasAcc = r.Header["X-Tossinvest-Account"]
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := New(srv.URL, 5*time.Second)
	if _, err := c.GetAccounts(context.Background(), Credentials{Authorization: "Bearer abc"}); err != nil {
		t.Fatal(err)
	}
	if hasAcc {
		t.Fatal("accounts 진입점은 계좌 헤더를 보내지 않아야 한다")
	}
}

func TestParseRateLimit_RetryAfter(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "3")
	if rl := parseRateLimit(h); rl.RetryAfter != 3*time.Second {
		t.Fatalf("retryAfter=%v, want 3s", rl.RetryAfter)
	}
}

func TestClientIDFromAuth(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"client-xyz"}`))
	tok := "Bearer header." + payload + ".signature"
	if got := ClientIDFromAuth(tok); got != "client-xyz" {
		t.Fatalf("sub=%q, want client-xyz", got)
	}
	if got := ClientIDFromAuth("Bearer not-a-jwt"); got != "" {
		t.Fatalf("잘못된 JWT 는 빈 문자열, got %q", got)
	}
	if got := ClientIDFromAuth(""); got != "" {
		t.Fatalf("빈 헤더는 빈 문자열, got %q", got)
	}
}

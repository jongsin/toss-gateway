package tossclient

import (
	"encoding/base64"
	"strings"
	"testing"
)

// makeJWT 는 서명 검증을 하지 않는 게이트웨이용 더미 JWT(header.payload.sig)를 만든다.
func makeJWT(payloadJSON string) string {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	return enc(`{"alg":"none","typ":"JWT"}`) + "." + enc(payloadJSON) + ".sig"
}

func TestClientIDFromAuth_ExtractsSub(t *testing.T) {
	if got := ClientIDFromAuth("Bearer " + makeJWT(`{"sub":"client-123"}`)); got != "client-123" {
		t.Fatalf("sub = %q, want client-123", got)
	}
	// Bearer 접두사 없이도 동작
	if got := ClientIDFromAuth(makeJWT(`{"sub":"naked"}`)); got != "naked" {
		t.Fatalf("sub = %q, want naked", got)
	}
}

// SEC-02: 과도하게 긴 sub 는 maxClientIDLen 으로 절단되어 키 메모리를 유계로 만든다.
func TestClientIDFromAuth_TruncatesLongSub(t *testing.T) {
	longSub := strings.Repeat("x", maxClientIDLen+50)
	got := ClientIDFromAuth(makeJWT(`{"sub":"` + longSub + `"}`))
	if len(got) != maxClientIDLen {
		t.Fatalf("절단 길이 = %d, want %d", len(got), maxClientIDLen)
	}
}

// SEC-02: maxJWTSegmentLen 을 넘는 거대한 payload segment 는 디코드하지 않고 빈 문자열(익명 폴백).
func TestClientIDFromAuth_RejectsHugeSegment(t *testing.T) {
	huge := strings.Repeat("A", maxJWTSegmentLen+1) // 유효 base64 문자, 길이만 초과
	token := "header." + huge + ".sig"
	if got := ClientIDFromAuth(token); got != "" {
		t.Fatalf("거대 segment 는 빈 문자열이어야 한다, got len=%d", len(got))
	}
	// 상한 이내(작은 payload)는 정상 처리됨을 대조 확인
	if got := ClientIDFromAuth(makeJWT(`{"sub":"ok"}`)); got != "ok" {
		t.Fatalf("정상 토큰 sub = %q, want ok", got)
	}
}

func TestClientIDFromAuth_InvalidReturnsEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "Bearer ", "nodots", "Bearer nodots"} {
		if got := ClientIDFromAuth(in); got != "" {
			t.Fatalf("입력 %q → %q, 빈 문자열이어야 한다", in, got)
		}
	}
}

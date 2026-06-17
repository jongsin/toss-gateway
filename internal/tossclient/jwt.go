package tossclient

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

const (
	// maxJWTSegmentLen 은 디코드를 시도할 JWT payload segment 의 최대 길이(바이트)이다.
	// Authorization 헤더는 MaxHeaderBytes(1 MiB)까지 허용되므로, 이 상한이 없으면
	// 거대한 payload 디코드/언마샬에 CPU·메모리를 낭비할 수 있다(SEC-02 증폭 방어).
	maxJWTSegmentLen = 4096
	// maxClientIDLen 은 rate limit 키로 쓰는 sub 의 최대 길이이다. 위조 토큰이 매우 긴
	// sub 로 거대한 맵 키를 만들어 메모리를 잠식하는 것을 막는다(SEC-02).
	maxClientIDLen = 128
)

// ClientIDFromAuth 는 Authorization 헤더의 JWT payload 에서 sub(client_id) 를 추출한다.
//
// 주의: 서명을 검증하지 않는다. 이 값은 오직 rate limit 버킷 키 용도로만 사용하며,
// 보안 결정(인가)에는 사용하지 않는다. 파싱 실패 시 빈 문자열을 반환한다.
// 키 메모리를 유계로 두기 위해 segment 길이와 sub 길이에 상한을 둔다(SEC-02).
func ClientIDFromAuth(authorization string) string {
	token := strings.TrimSpace(authorization)
	if token == "" {
		return ""
	}
	if fields := strings.Fields(token); len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") {
		token = fields[1]
	}
	segs := strings.Split(token, ".")
	if len(segs) < 2 {
		return ""
	}
	if len(segs[1]) > maxJWTSegmentLen {
		return "" // 비정상적으로 큰 payload: 익명 폴백으로 처리
	}
	payload, err := decodeSegment(segs[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	sub := claims.Sub
	if len(sub) > maxClientIDLen {
		sub = sub[:maxClientIDLen] // 키 길이 상한
	}
	return sub
}

func decodeSegment(seg string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(seg); err == nil {
		return b, nil
	}
	// 패딩이 있는 경우 대비
	return base64.URLEncoding.DecodeString(seg)
}

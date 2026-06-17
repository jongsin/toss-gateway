// Package i18n 는 게이트웨이가 자체 생성하는 메시지의 다국어(ko/en) 처리를 담당한다.
// 토스 원본 에러 메시지는 그대로 전달하고, 게이트웨이 레이어 메시지만 번역한다.
package i18n

import "strings"

// 지원 언어
const (
	LangKO = "ko"
	LangEN = "en"
)

var supported = map[string]bool{LangKO: true, LangEN: true}

// Resolve 는 명시적 lang(쿼리 ?lang=), Accept-Language 헤더, 기본값 순으로 언어를 결정한다.
func Resolve(explicit, acceptLanguage, def string) string {
	if l := normalize(explicit); l != "" {
		return l
	}
	// Accept-Language: "ko-KR,ko;q=0.9,en;q=0.8" → 첫 지원 언어 선택
	for _, part := range strings.Split(acceptLanguage, ",") {
		tag := strings.TrimSpace(part)
		if i := strings.Index(tag, ";"); i >= 0 {
			tag = tag[:i]
		}
		if l := normalize(tag); l != "" {
			return l
		}
	}
	if supported[def] {
		return def
	}
	return LangKO
}

func normalize(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return ""
	}
	if i := strings.IndexAny(tag, "-_"); i >= 0 {
		tag = tag[:i]
	}
	if supported[tag] {
		return tag
	}
	return ""
}

// 게이트웨이 자체 에러/메시지 코드 → 언어별 메시지.
var messages = map[string]map[string]string{
	LangKO: {
		"rate-limit-exceeded":     "요청이 너무 많습니다. Retry-After 헤더의 초만큼 기다린 뒤 다시 시도해주세요.",
		"invalid-request":         "요청이 유효하지 않습니다.",
		"missing-parameter":       "필수 파라미터가 누락되었습니다.",
		"account-header-required": "계좌 식별 헤더(X-Tossinvest-Account)가 필요합니다.",
		"unauthorized":            "인증 토큰이 필요합니다. Authorization: Bearer 헤더를 전달해주세요.",
		"invalid-account":         "계좌 식별자(accountSeq)는 양의 정수여야 합니다.",
		"upstream-unavailable":    "토스 서버에 연결할 수 없습니다. 잠시 후 다시 시도해주세요.",
		"upstream-timeout":        "토스 서버 응답이 지연되고 있습니다. 잠시 후 다시 시도해주세요.",
		"payload-too-large":       "요청 본문이 허용 크기를 초과했습니다.",
		"not-found":               "지원하지 않는 API 경로입니다.",
		"method-not-allowed":      "허용되지 않은 HTTP 메서드입니다.",
		"internal-error":          "게이트웨이 내부 오류가 발생했습니다.",
	},
	LangEN: {
		"rate-limit-exceeded":     "Too many requests. Retry after the number of seconds in the Retry-After header.",
		"invalid-request":         "The request is invalid.",
		"missing-parameter":       "A required parameter is missing.",
		"account-header-required": "The account header (X-Tossinvest-Account) is required.",
		"unauthorized":            "Authentication token required. Pass an Authorization: Bearer header.",
		"invalid-account":         "The account identifier (accountSeq) must be a positive integer.",
		"upstream-unavailable":    "Unable to reach the Toss server. Please try again shortly.",
		"upstream-timeout":        "The Toss server is responding slowly. Please try again shortly.",
		"payload-too-large":       "The request body exceeds the allowed size.",
		"not-found":               "The requested API path is not supported.",
		"method-not-allowed":      "HTTP method not allowed.",
		"internal-error":          "An internal gateway error occurred.",
	},
}

// Translate 는 주어진 언어의 코드 메시지를 반환한다. 없으면 fallback, 그것도 없으면 ko, 최후엔 code 자체를 반환한다.
func Translate(lang, code, fallback string) string {
	if m, ok := messages[lang]; ok {
		if msg, ok := m[code]; ok {
			return msg
		}
	}
	if fallback != "" {
		return fallback
	}
	if msg, ok := messages[LangKO][code]; ok {
		return msg
	}
	return code
}

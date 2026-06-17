// Package openapi 는 게이트웨이 자체 OpenAPI 스펙과 Swagger UI 페이지를 임베드(go:embed)하여 제공한다.
//
// Swagger UI 정적 자산(CSS/JS, 버전 v5.17.14)은 외부 CDN(jsdelivr) 대신 게이트웨이
// 바이너리에 동봉하여 동일 출처로 셀프 호스팅한다(SEC-04 조치). CDN 공급망 침해/SRI
// 부재 위험을 제거하고 완전한 에어갭 배포를 지원한다.
//
// 자산 출처 : https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.17.14/
// 라이선스   : Apache License 2.0, © SmartBear Software (assets/LICENSE.md 참조)
package openapi

import (
	_ "embed"
	"strings"
)

//go:embed openapi.yaml
var SpecYAML []byte

//go:embed swagger.html
var swaggerHTML string

// SwaggerCSS 는 셀프 호스팅하는 Swagger UI 스타일시트(v5.17.14)이다.
//
//go:embed assets/swagger-ui.css
var SwaggerCSS []byte

// SwaggerBundleJS 는 셀프 호스팅하는 Swagger UI 번들 스크립트(v5.17.14)이다.
//
//go:embed assets/swagger-ui-bundle.js
var SwaggerBundleJS []byte

// SwaggerInitJS 는 인라인 부트스트랩을 대체하는 셀프 호스팅 초기화 스크립트이다.
// 인라인 <script> 제거로 /docs CSP 의 script-src 를 'self' 로 좁히기 위함이다(N-B 보강).
// (게이트웨이 자체 코드이며 Swagger UI 동봉 자산이 아니다 → assets/ 가 아닌 패키지 루트에 둔다.)
//
//go:embed swagger-init.js
var SwaggerInitJS []byte

// SwaggerUI 는 spec/css/js/init URL 을 주입한 Swagger UI HTML 을 반환한다.
// css/js/init 은 게이트웨이가 셀프 호스팅하는 동일 출처 경로를 가리킨다(외부 CDN 미사용).
func SwaggerUI(specURL, cssURL, jsURL, initURL string) []byte {
	html := swaggerHTML
	html = strings.ReplaceAll(html, "__SPEC_URL__", specURL)
	html = strings.ReplaceAll(html, "__CSS_URL__", cssURL)
	html = strings.ReplaceAll(html, "__JS_URL__", jsURL)
	html = strings.ReplaceAll(html, "__INIT_URL__", initURL)
	return []byte(html)
}

// Swagger UI 부트스트랩 (셀프 호스팅).
//
// 인라인 <script> 를 제거하여 /docs 응답 CSP 의 script-src 를 'self' 로 좁히기 위한
// 외부 스크립트다(N-B 보강). 스펙 URL 은 이 <script> 태그의 data-spec-url 속성으로
// 주입된다 — HTML 속성 값은 CSP script-src 의 적용 대상이 아니므로 'unsafe-inline'
// 없이도 동작한다. 동일 출처(/docs/swagger-init.js)에서 서빙된다.
(function () {
  "use strict";
  // 외부 클래식 스크립트 실행 시점에는 document.currentScript 가 이 태그를 가리킨다.
  var tag = document.currentScript;
  var specURL = (tag && tag.dataset && tag.dataset.specUrl) || "";
  window.addEventListener("load", function () {
    window.ui = SwaggerUIBundle({
      url: specURL,
      dom_id: "#swagger-ui",
      deepLinking: true,
      presets: [SwaggerUIBundle.presets.apis],
      layout: "BaseLayout",
      tryItOutEnabled: true,
      persistAuthorization: true
    });
  });
})();

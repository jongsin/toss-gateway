package tossclient

// 본 파일은 게이트웨이가 타입 검증/문서화에 사용하는 요청 모델을 정의한다.
// 응답은 무손실 전달(json.RawMessage)하므로 응답 모델은 정의하지 않는다.
// (전체 응답 스키마는 임베드된 OpenAPI 문서에서 제공한다.)

// TokenRequest 는 OAuth2 Client Credentials Grant 토큰 발급 요청이다.
type TokenRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// OrderCreateRequest 는 주문 생성 요청이다.
// 토스 스펙은 수량기반/금액기반(oneOf)으로 나뉘며, 본 구조체는 두 변형을 모두 표현한다.
// (검증은 게이트웨이 validate 레이어에서 수행한다.)
type OrderCreateRequest struct {
	ClientOrderID         string `json:"clientOrderId,omitempty"`         // 멱등성 키 (최대 36자)
	Symbol                string `json:"symbol"`                          // 종목 심볼
	Side                  string `json:"side"`                            // BUY | SELL
	OrderType             string `json:"orderType"`                       // LIMIT | MARKET
	TimeInForce           string `json:"timeInForce,omitempty"`           // DAY | CLS
	Quantity              string `json:"quantity,omitempty"`              // 수량기반: 정수
	OrderAmount           string `json:"orderAmount,omitempty"`           // 금액기반(US MARKET): 달러
	Price                 string `json:"price,omitempty"`                 // LIMIT 시 필수
	ConfirmHighValueOrder bool   `json:"confirmHighValueOrder,omitempty"` // 1억 이상 주문 확인
}

// OrderModifyRequest 는 주문 정정 요청이다.
type OrderModifyRequest struct {
	OrderType             string `json:"orderType"`          // LIMIT | MARKET
	Quantity              string `json:"quantity,omitempty"` // KR 필수 / US 전달불가
	Price                 string `json:"price,omitempty"`    // LIMIT 시 필수
	ConfirmHighValueOrder bool   `json:"confirmHighValueOrder,omitempty"`
}

// TradesParams 는 최근 체결 내역 조회 파라미터이다.
type TradesParams struct {
	Symbol string // 필수
	Count  int    // 1~50 (0=미지정)
}

// CandlesParams 는 캔들 차트 조회 파라미터이다.
type CandlesParams struct {
	Symbol   string // 필수
	Interval string // 1m | 1d (필수)
	Count    int    // 1~200 (0=미지정)
	Before   string // date-time (선택)
	Adjusted *bool  // 수정주가 여부 (nil=미지정)
}

// OrdersParams 는 주문 목록 조회 파라미터이다.
type OrdersParams struct {
	Status string // OPEN | CLOSED (필수)
	Symbol string
	From   string
	To     string
	Cursor string
	Limit  int
}

// ExchangeRateParams 는 환율 조회 파라미터이다.
type ExchangeRateParams struct {
	BaseCurrency  string // 필수
	QuoteCurrency string // 필수
	DateTime      string // 선택
}

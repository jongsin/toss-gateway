package tossclient

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// ===== Auth =====

// IssueToken 은 OAuth2 Client Credentials Grant 로 액세스 토큰을 발급한다. (RL group: AUTH)
// POST /oauth2/token
func (c *Client) IssueToken(ctx context.Context, req TokenRequest) (*Response, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", req.ClientID)
	form.Set("client_secret", req.ClientSecret)
	return c.do(ctx, "POST", "/oauth2/token", nil, Credentials{}, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
}

// ===== Market Data =====

// GetOrderbook: GET /api/v1/orderbook (RL: MARKET_DATA)
func (c *Client) GetOrderbook(ctx context.Context, cred Credentials, symbol string) (*Response, error) {
	q := url.Values{}
	q.Set("symbol", symbol)
	return c.get(ctx, cred, "/api/v1/orderbook", q)
}

// GetPrices: GET /api/v1/prices (RL: MARKET_DATA). symbols 는 콤마 구분(최대 200).
func (c *Client) GetPrices(ctx context.Context, cred Credentials, symbols string) (*Response, error) {
	q := url.Values{}
	q.Set("symbols", symbols)
	return c.get(ctx, cred, "/api/v1/prices", q)
}

// GetTrades: GET /api/v1/trades (RL: MARKET_DATA)
func (c *Client) GetTrades(ctx context.Context, cred Credentials, p TradesParams) (*Response, error) {
	q := url.Values{}
	q.Set("symbol", p.Symbol)
	if p.Count > 0 {
		q.Set("count", strconv.Itoa(p.Count))
	}
	return c.get(ctx, cred, "/api/v1/trades", q)
}

// GetPriceLimits: GET /api/v1/price-limits (RL: MARKET_DATA)
func (c *Client) GetPriceLimits(ctx context.Context, cred Credentials, symbol string) (*Response, error) {
	q := url.Values{}
	q.Set("symbol", symbol)
	return c.get(ctx, cred, "/api/v1/price-limits", q)
}

// GetCandles: GET /api/v1/candles (RL: MARKET_DATA_CHART)
func (c *Client) GetCandles(ctx context.Context, cred Credentials, p CandlesParams) (*Response, error) {
	q := url.Values{}
	q.Set("symbol", p.Symbol)
	q.Set("interval", p.Interval)
	if p.Count > 0 {
		q.Set("count", strconv.Itoa(p.Count))
	}
	if p.Before != "" {
		q.Set("before", p.Before)
	}
	if p.Adjusted != nil {
		q.Set("adjusted", strconv.FormatBool(*p.Adjusted))
	}
	return c.get(ctx, cred, "/api/v1/candles", q)
}

// ===== Stock Info =====

// GetStocks: GET /api/v1/stocks (RL: STOCK). symbols 는 콤마 구분(최대 200).
func (c *Client) GetStocks(ctx context.Context, cred Credentials, symbols string) (*Response, error) {
	q := url.Values{}
	q.Set("symbols", symbols)
	return c.get(ctx, cred, "/api/v1/stocks", q)
}

// GetStockWarnings: GET /api/v1/stocks/{symbol}/warnings (RL: STOCK)
func (c *Client) GetStockWarnings(ctx context.Context, cred Credentials, symbol string) (*Response, error) {
	path := "/api/v1/stocks/" + url.PathEscape(symbol) + "/warnings"
	return c.get(ctx, cred, path, nil)
}

// ===== Market Info =====

// GetExchangeRate: GET /api/v1/exchange-rate (RL: MARKET_INFO)
func (c *Client) GetExchangeRate(ctx context.Context, cred Credentials, p ExchangeRateParams) (*Response, error) {
	q := url.Values{}
	q.Set("baseCurrency", p.BaseCurrency)
	q.Set("quoteCurrency", p.QuoteCurrency)
	if p.DateTime != "" {
		q.Set("dateTime", p.DateTime)
	}
	return c.get(ctx, cred, "/api/v1/exchange-rate", q)
}

// GetKrMarketCalendar: GET /api/v1/market-calendar/KR (RL: MARKET_INFO)
func (c *Client) GetKrMarketCalendar(ctx context.Context, cred Credentials, date string) (*Response, error) {
	q := url.Values{}
	if date != "" {
		q.Set("date", date)
	}
	return c.get(ctx, cred, "/api/v1/market-calendar/KR", q)
}

// GetUsMarketCalendar: GET /api/v1/market-calendar/US (RL: MARKET_INFO)
func (c *Client) GetUsMarketCalendar(ctx context.Context, cred Credentials, date string) (*Response, error) {
	q := url.Values{}
	if date != "" {
		q.Set("date", date)
	}
	return c.get(ctx, cred, "/api/v1/market-calendar/US", q)
}

// ===== Account =====

// GetAccounts: GET /api/v1/accounts (RL: ACCOUNT). 계좌 헤더 불필요(진입점).
func (c *Client) GetAccounts(ctx context.Context, cred Credentials) (*Response, error) {
	return c.get(ctx, cred, "/api/v1/accounts", nil)
}

// ===== Asset =====

// GetHoldings: GET /api/v1/holdings (RL: ASSET). 계좌 헤더 필요. symbol 선택.
func (c *Client) GetHoldings(ctx context.Context, cred Credentials, symbol string) (*Response, error) {
	q := url.Values{}
	if symbol != "" {
		q.Set("symbol", symbol)
	}
	return c.get(ctx, cred, "/api/v1/holdings", q)
}

// ===== Order =====

// CreateOrder: POST /api/v1/orders (RL: ORDER). 계좌 헤더 필요. body 는 검증된 원본 JSON.
func (c *Client) CreateOrder(ctx context.Context, cred Credentials, body []byte) (*Response, error) {
	return c.postJSON(ctx, cred, "/api/v1/orders", body)
}

// ModifyOrder: POST /api/v1/orders/{orderId}/modify (RL: ORDER). 계좌 헤더 필요.
func (c *Client) ModifyOrder(ctx context.Context, cred Credentials, orderID string, body []byte) (*Response, error) {
	path := "/api/v1/orders/" + url.PathEscape(orderID) + "/modify"
	return c.postJSON(ctx, cred, path, body)
}

// CancelOrder: POST /api/v1/orders/{orderId}/cancel (RL: ORDER). 계좌 헤더 필요. body 없음.
func (c *Client) CancelOrder(ctx context.Context, cred Credentials, orderID string) (*Response, error) {
	path := "/api/v1/orders/" + url.PathEscape(orderID) + "/cancel"
	return c.do(ctx, "POST", path, nil, cred, nil, "")
}

// ===== Order History =====

// GetOrders: GET /api/v1/orders (RL: ORDER_HISTORY). 계좌 헤더 필요.
func (c *Client) GetOrders(ctx context.Context, cred Credentials, p OrdersParams) (*Response, error) {
	q := url.Values{}
	q.Set("status", p.Status)
	if p.Symbol != "" {
		q.Set("symbol", p.Symbol)
	}
	if p.From != "" {
		q.Set("from", p.From)
	}
	if p.To != "" {
		q.Set("to", p.To)
	}
	if p.Cursor != "" {
		q.Set("cursor", p.Cursor)
	}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	return c.get(ctx, cred, "/api/v1/orders", q)
}

// GetOrder: GET /api/v1/orders/{orderId} (RL: ORDER_HISTORY). 계좌 헤더 필요.
func (c *Client) GetOrder(ctx context.Context, cred Credentials, orderID string) (*Response, error) {
	path := "/api/v1/orders/" + url.PathEscape(orderID)
	return c.get(ctx, cred, path, nil)
}

// ===== Order Info =====

// GetBuyingPower: GET /api/v1/buying-power (RL: ORDER_INFO). 계좌 헤더 필요.
func (c *Client) GetBuyingPower(ctx context.Context, cred Credentials, currency string) (*Response, error) {
	q := url.Values{}
	q.Set("currency", currency)
	return c.get(ctx, cred, "/api/v1/buying-power", q)
}

// GetSellableQuantity: GET /api/v1/sellable-quantity (RL: ORDER_INFO). 계좌 헤더 필요.
func (c *Client) GetSellableQuantity(ctx context.Context, cred Credentials, symbol string) (*Response, error) {
	q := url.Values{}
	q.Set("symbol", symbol)
	return c.get(ctx, cred, "/api/v1/sellable-quantity", q)
}

// GetCommissions: GET /api/v1/commissions (RL: ORDER_INFO). 계좌 헤더 필요.
func (c *Client) GetCommissions(ctx context.Context, cred Credentials) (*Response, error) {
	return c.get(ctx, cred, "/api/v1/commissions", nil)
}

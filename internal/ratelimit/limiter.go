// Package ratelimit 는 토스 Open API 의 "클라이언트 × API 그룹" 단위 초당 요청 수(TPS)
// 한도를 토스로 요청을 보내기 전에 사전 게이팅하여 한도를 절대 초과하지 않도록 한다.
//
// 외부 의존성 없이 표준 라이브러리만으로 구현한 토큰 버킷 기반 리미터이다.
package ratelimit

import (
	"sync"
	"time"
)

// Group 은 토스 Rate Limits Group 의 한도 정의이다.
type Group struct {
	Name      string  // 그룹명 (예: ORDER)
	Base      float64 // 평상시 한도 (TPS)
	Peak      float64 // 피크시간 한도 (TPS). 0 이면 피크 차등 없음.
	PeakStart int     // 피크 시작 (KST 분 단위, 예: 9*60 = 540)
	PeakEnd   int     // 피크 종료 (KST 분 단위)
}

// 토스증권 Open API 문서(overview.md)에 명시된 그룹별 한도.
// 한도는 운영 상황에 따라 조정될 수 있으며, 응답 헤더(X-RateLimit-Limit)로 적응한다.
var DefaultGroups = map[string]Group{
	"AUTH":              {Name: "AUTH", Base: 5},
	"ACCOUNT":           {Name: "ACCOUNT", Base: 1},
	"ASSET":             {Name: "ASSET", Base: 5},
	"STOCK":             {Name: "STOCK", Base: 5},
	"MARKET_INFO":       {Name: "MARKET_INFO", Base: 3},
	"MARKET_DATA":       {Name: "MARKET_DATA", Base: 10},
	"MARKET_DATA_CHART": {Name: "MARKET_DATA_CHART", Base: 5},
	"ORDER":             {Name: "ORDER", Base: 6, Peak: 3, PeakStart: 9 * 60, PeakEnd: 9*60 + 10},
	"ORDER_HISTORY":     {Name: "ORDER_HISTORY", Base: 5},
	"ORDER_INFO":        {Name: "ORDER_INFO", Base: 6, Peak: 3, PeakStart: 9 * 60, PeakEnd: 9*60 + 10},
}

// bucket 은 단일 (client × group) 토큰 버킷이다.
type bucket struct {
	mu           sync.Mutex
	tokens       float64
	capacity     float64
	rate         float64
	last         time.Time
	blockedUntil time.Time // 429 수신 시 Retry-After 만큼 차단
	lastSeen     time.Time // GC 용
	adaptLimit   float64   // 응답 헤더 기반 적응 한도 (0=미적용)
}

// allow 는 현재 시각/한도로 토큰을 1개 소비 시도한다.
func (b *bucket) allow(now time.Time, capacity, rate float64) (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.lastSeen = now
	if now.Before(b.blockedUntil) {
		return false, b.blockedUntil.Sub(now)
	}
	if capacity < 1 {
		capacity = 1
	}
	if rate <= 0 {
		rate = capacity
	}
	b.capacity, b.rate = capacity, rate

	if b.last.IsZero() {
		b.last = now
		b.tokens = capacity
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens += elapsed * rate
		if b.tokens > capacity {
			b.tokens = capacity
		}
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens -= 1
		return true, 0
	}
	need := 1 - b.tokens
	return false, time.Duration(need / rate * float64(time.Second))
}

// Limiter 는 (client × group) 별 토큰 버킷을 관리한다. 동시성 안전하다.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	groups  map[string]Group
	safety  float64
	enabled bool
	loc     *time.Location
	now     func() time.Time
}

// New 는 문서 기본 한도로 리미터를 생성한다. safety 는 0<r<=1 안전계수이다.
func New(safety float64, enabled bool) *Limiter {
	if safety <= 0 || safety > 1 {
		safety = 1
	}
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		// tzdata 부재 환경 대비: KST(UTC+9) 고정 오프셋.
		loc = time.FixedZone("KST", 9*60*60)
	}
	groups := make(map[string]Group, len(DefaultGroups))
	for k, v := range DefaultGroups {
		groups[k] = v
	}
	return &Limiter{
		buckets: make(map[string]*bucket),
		groups:  groups,
		safety:  safety,
		enabled: enabled,
		loc:     loc,
		now:     time.Now,
	}
}

// withClock 는 테스트용 시계 주입이다.
func (l *Limiter) withClock(f func() time.Time) *Limiter {
	l.now = f
	return l
}

// effectiveLimit 은 그룹/시각/적응한도/안전계수를 반영한 현재 허용 TPS 를 반환한다.
func (l *Limiter) effectiveLimit(g Group, b *bucket, now time.Time) float64 {
	limit := g.Base
	if g.Peak > 0 {
		t := now.In(l.loc)
		minutes := t.Hour()*60 + t.Minute()
		if minutes >= g.PeakStart && minutes < g.PeakEnd {
			limit = g.Peak
		}
	}
	if b != nil && b.adaptLimit > 0 && b.adaptLimit < limit {
		limit = b.adaptLimit // 토스가 통보한 실제 한도가 더 낮으면 그것을 따른다.
	}
	return limit * l.safety
}

// Allow 는 (clientID, group) 요청 1건을 허용할지 결정한다.
// 반환: 허용 여부, (불허 시) 권장 대기시간.
// 비활성화(enabled=false) 또는 미지정 그룹은 항상 허용한다.
func (l *Limiter) Allow(clientID, group string) (bool, time.Duration) {
	if !l.enabled {
		return true, 0
	}
	g, ok := l.groups[group]
	if !ok {
		return true, 0
	}
	now := l.now()
	b := l.getBucket(clientID, group, now)
	limit := l.effectiveLimit(g, b, now)
	return b.allow(now, limit, limit)
}

// maxBuckets 는 rate limit 버킷 맵의 최대 엔트리 수이다. 위조 JWT sub 나 위조 XFF 로
// distinct 키를 대량 생성하는 메모리 고갈 DoS(SEC-02)를 방어한다. 초과 시
// evictLocked 로 표본 기반 근사 LRU 회수를 수행한다.
// 키 길이는 tossclient 단에서 상한(maxClientIDLen)을 두므로 엔트리당 메모리도 유계이다.
const maxBuckets = 50000

func (l *Limiter) getBucket(clientID, group string, now time.Time) *bucket {
	key := clientID + "|" + group
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxBuckets {
			l.evictLocked(now)
		}
		b = &bucket{lastSeen: now}
		l.buckets[key] = b
	}
	return b
}

// evictLocked 는 맵이 가득 찼을 때 표본(최대 sampleSize 개) 중 가장 오래 미사용된
// 비차단 버킷 하나를 제거한다. 무작위 맵 순회를 이용한 근사 LRU 로 O(sampleSize) 이며,
// 호출자는 l.mu 를 보유해야 한다(락 순서 l.mu→b.mu, GC/getBucket 과 일관).
// 표본이 전부 429 차단 상태면 가장 먼저 해제될 버킷을 제거하여 차단 정보 손실을 최소화한다.
func (l *Limiter) evictLocked(now time.Time) {
	const sampleSize = 8
	var (
		victimKey  string
		oldestSeen time.Time
		blockedKey string
		soonest    time.Time
		n          int
	)
	for key, b := range l.buckets {
		b.mu.Lock()
		blocked := now.Before(b.blockedUntil)
		seen := b.lastSeen
		until := b.blockedUntil
		b.mu.Unlock()
		if blocked {
			if blockedKey == "" || until.Before(soonest) {
				blockedKey, soonest = key, until
			}
		} else if victimKey == "" || seen.Before(oldestSeen) {
			victimKey, oldestSeen = key, seen
		}
		if n++; n >= sampleSize {
			break
		}
	}
	if victimKey == "" {
		victimKey = blockedKey
	}
	if victimKey != "" {
		delete(l.buckets, victimKey)
	}
}

// Observe 는 토스 응답 헤더로부터 한도를 적응시킨다.
//   - limitHeader: X-RateLimit-Limit (>0 이고 문서 한도보다 낮으면 적응)
//   - retryAfter: 429 응답의 Retry-After (>0 이면 해당 키를 그 시간만큼 차단)
func (l *Limiter) Observe(clientID, group string, limitHeader float64, retryAfter time.Duration) {
	if !l.enabled {
		return
	}
	if _, ok := l.groups[group]; !ok {
		return
	}
	now := l.now()
	b := l.getBucket(clientID, group, now)
	b.mu.Lock()
	defer b.mu.Unlock()
	if limitHeader > 0 {
		b.adaptLimit = limitHeader
	}
	if retryAfter > 0 {
		b.blockedUntil = now.Add(retryAfter)
		b.tokens = 0
	}
}

// Refund 는 사전 게이팅(Allow)에서 소비한 토큰 1개를 해당 키 버킷에 돌려준다.
// 업스트림이 인증 실패(401/403)를 반환한 경우에 호출하여, 위조 JWT sub 로 타인의
// 버킷을 고갈시키는 교차 테넌트 DoS(SEC-01)를 완화한다. 정당한 사용자의 만료 토큰
// 재시도도 한도를 소모하지 않게 되어 UX 도 개선된다. 버킷이 없거나 비활성/미지정
// 그룹이면 무시한다.
//
// 주의: AUTH(토큰 발급) 경로에는 적용하지 않는다. 인증 실패를 환급하면 토스 인증
// 엔드포인트에 대한 자격증명 스터핑이 게이트웨이 한도를 우회할 수 있기 때문이다.
func (l *Limiter) Refund(clientID, group string) {
	if !l.enabled {
		return
	}
	if _, ok := l.groups[group]; !ok {
		return
	}
	l.mu.Lock()
	b, ok := l.buckets[clientID+"|"+group]
	l.mu.Unlock()
	if ok {
		b.refund()
	}
}

func (b *bucket) refund() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.capacity <= 0 {
		return // 아직 allow 된 적 없는 버킷: 환급할 토큰 없음
	}
	if b.tokens += 1; b.tokens > b.capacity {
		b.tokens = b.capacity
	}
}

// GC 는 maxIdle 이상 미사용 버킷을 제거한다. 주기적으로 호출한다.
func (l *Limiter) GC(maxIdle time.Duration) int {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	removed := 0
	for key, b := range l.buckets {
		b.mu.Lock()
		idle := now.Sub(b.lastSeen)
		blocked := now.Before(b.blockedUntil)
		b.mu.Unlock()
		if idle > maxIdle && !blocked {
			delete(l.buckets, key)
			removed++
		}
	}
	return removed
}

// StartJanitor 는 every 주기로 GC 를 수행하는 고루틴을 시작한다. stop 채널로 종료한다.
func (l *Limiter) StartJanitor(every, maxIdle time.Duration, stop <-chan struct{}) {
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				l.GC(maxIdle)
			case <-stop:
				return
			}
		}
	}()
}

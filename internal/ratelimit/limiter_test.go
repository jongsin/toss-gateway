package ratelimit

import (
	"strconv"
	"testing"
	"time"
)

// 비피크 시각: 평일 13:00 KST = 04:00 UTC (ORDER 피크 09:00~09:10 KST 밖)
func offPeak() time.Time { return time.Date(2025, 1, 2, 4, 0, 0, 0, time.UTC) }

func TestAllow_TokenBucketRefill(t *testing.T) {
	now := offPeak()
	l := New(1.0, true).withClock(func() time.Time { return now })

	// ACCOUNT = 1 TPS → 첫 요청 허용
	if ok, _ := l.Allow("client-a", "ACCOUNT"); !ok {
		t.Fatal("첫 요청은 허용되어야 한다")
	}
	// 즉시 두 번째 요청 → 거부 + 양의 대기시간
	ok, retry := l.Allow("client-a", "ACCOUNT")
	if ok {
		t.Fatal("동일 초 내 두 번째 요청은 거부되어야 한다")
	}
	if retry <= 0 {
		t.Fatalf("거부 시 양의 retry 가 필요하다, got %v", retry)
	}
	// 1초 경과 후 → 토큰 충전되어 다시 허용
	now = now.Add(time.Second)
	if ok, _ := l.Allow("client-a", "ACCOUNT"); !ok {
		t.Fatal("1초 후에는 다시 허용되어야 한다")
	}
}

// 멀티유저 격리: 한 클라이언트가 소진해도 다른 클라이언트는 영향받지 않는다.
func TestAllow_PerClientIsolation(t *testing.T) {
	now := offPeak()
	l := New(1.0, true).withClock(func() time.Time { return now })

	l.Allow("client-a", "ACCOUNT")
	if ok, _ := l.Allow("client-a", "ACCOUNT"); ok {
		t.Fatal("client-a 는 두 번째에서 거부되어야 한다")
	}
	if ok, _ := l.Allow("client-b", "ACCOUNT"); !ok {
		t.Fatal("client-b 는 독립 버킷이므로 허용되어야 한다")
	}
}

func TestAllow_DisabledAlwaysAllows(t *testing.T) {
	l := New(1.0, false)
	for range 50 {
		if ok, _ := l.Allow("c", "ACCOUNT"); !ok {
			t.Fatal("비활성 리미터는 항상 허용해야 한다")
		}
	}
}

func TestAllow_UnknownGroupAllows(t *testing.T) {
	l := New(1.0, true)
	if ok, _ := l.Allow("c", "NOT_A_GROUP"); !ok {
		t.Fatal("미정의 그룹은 게이팅하지 않고 허용해야 한다")
	}
}

func TestEffectiveLimit_PeakHours(t *testing.T) {
	l := New(1.0, true)
	g := DefaultGroups["ORDER"] // Base 6, Peak 3 @ 09:00~09:10 KST

	// 09:05 KST = 00:05 UTC → 피크
	peak := time.Date(2025, 1, 2, 0, 5, 0, 0, time.UTC)
	if got := l.effectiveLimit(g, nil, peak); got != 3 {
		t.Fatalf("피크 한도 = %v, want 3", got)
	}
	// 13:00 KST = 04:00 UTC → 평상시
	if got := l.effectiveLimit(g, nil, offPeak()); got != 6 {
		t.Fatalf("평상시 한도 = %v, want 6", got)
	}
}

func TestEffectiveLimit_SafetyRatio(t *testing.T) {
	l := New(0.5, true)               // 안전계수 50%
	g := DefaultGroups["MARKET_DATA"] // Base 10
	if got := l.effectiveLimit(g, nil, offPeak()); got != 5 {
		t.Fatalf("안전계수 적용 한도 = %v, want 5", got)
	}
}

func TestObserve_AdaptsToLowerLimit(t *testing.T) {
	now := offPeak()
	l := New(1.0, true).withClock(func() time.Time { return now })

	l.Allow("c", "MARKET_DATA")         // 버킷 생성 (Base 10)
	l.Observe("c", "MARKET_DATA", 2, 0) // 토스가 limit=2 통보 → 적응

	g := DefaultGroups["MARKET_DATA"]
	b := l.getBucket("c", "MARKET_DATA", now)
	if got := l.effectiveLimit(g, b, now); got != 2 {
		t.Fatalf("적응 한도 = %v, want 2", got)
	}
}

func TestObserve_RetryAfterBlocks(t *testing.T) {
	cur := offPeak()
	l := New(1.0, true).withClock(func() time.Time { return cur })

	l.Allow("c", "MARKET_DATA")
	l.Observe("c", "MARKET_DATA", 0, 2*time.Second) // 429 백오프

	if ok, retry := l.Allow("c", "MARKET_DATA"); ok || retry <= 0 {
		t.Fatalf("429 직후에는 차단되어야 한다, ok=%v retry=%v", ok, retry)
	}
	cur = cur.Add(2 * time.Second)
	if ok, _ := l.Allow("c", "MARKET_DATA"); !ok {
		t.Fatal("Retry-After 경과 후에는 다시 허용되어야 한다")
	}
}

func TestGC_RemovesIdleBuckets(t *testing.T) {
	cur := offPeak()
	l := New(1.0, true).withClock(func() time.Time { return cur })

	l.Allow("c", "ACCOUNT")
	cur = cur.Add(10 * time.Minute)
	if removed := l.GC(5 * time.Minute); removed != 1 {
		t.Fatalf("유휴 버킷 제거 수 = %d, want 1", removed)
	}
}

// SEC-01: 업스트림 401/403 시 환급하면 소비한 토큰이 복구되어 후속 요청이 막히지 않는다.
func TestRefund_RestoresConsumedToken(t *testing.T) {
	now := offPeak()
	l := New(1.0, true).withClock(func() time.Time { return now })

	if ok, _ := l.Allow("c", "ACCOUNT"); !ok { // ACCOUNT 1 TPS, 토큰 1 소비
		t.Fatal("첫 요청은 허용되어야 한다")
	}
	if ok, _ := l.Allow("c", "ACCOUNT"); ok { // 소진 → 거부
		t.Fatal("환급 전에는 두 번째가 거부되어야 한다")
	}
	l.Refund("c", "ACCOUNT") // 환급
	if ok, _ := l.Allow("c", "ACCOUNT"); !ok {
		t.Fatal("환급 후에는 다시 허용되어야 한다")
	}
}

// 환급은 capacity 를 초과해 토큰을 누적하지 않는다.
func TestRefund_DoesNotExceedCapacity(t *testing.T) {
	now := offPeak()
	l := New(1.0, true).withClock(func() time.Time { return now })

	l.Allow("c", "ACCOUNT") // 버킷 생성 + 1 소비 (capacity 1 → tokens 0)
	for range 5 {
		l.Refund("c", "ACCOUNT") // 과다 환급 시도
	}
	// capacity(1) 만큼만 복구되어야 하므로 1회만 허용, 그 다음은 거부
	if ok, _ := l.Allow("c", "ACCOUNT"); !ok {
		t.Fatal("환급된 1 토큰으로 한 번은 허용되어야 한다")
	}
	if ok, _ := l.Allow("c", "ACCOUNT"); ok {
		t.Fatal("capacity 초과 환급은 없어야 한다(두 번째 거부)")
	}
}

// 존재하지 않는 버킷/비활성/미정의 그룹 환급은 무해(no-op)하다.
func TestRefund_SafeNoops(t *testing.T) {
	now := offPeak()
	l := New(1.0, true).withClock(func() time.Time { return now })
	l.Refund("ghost", "ACCOUNT")           // 버킷 없음
	l.Refund("c", "NOT_A_GROUP")           // 미정의 그룹
	New(1.0, false).Refund("c", "ACCOUNT") // 비활성 리미터
}

// SEC-02: 버킷 맵은 maxBuckets 를 초과하지 않는다(표본 기반 evict).
func TestEviction_CapsBucketMap(t *testing.T) {
	now := offPeak()
	l := New(1.0, true).withClock(func() time.Time { return now })

	for i := range maxBuckets + 300 { // 상한보다 많이 distinct 키 생성
		l.Allow("client-"+strconv.Itoa(i), "MARKET_DATA")
	}
	l.mu.Lock()
	n := len(l.buckets)
	l.mu.Unlock()
	if n > maxBuckets {
		t.Fatalf("버킷 맵 크기 = %d, 상한 %d 초과(메모리 DoS 방어 실패)", n, maxBuckets)
	}
	if n != maxBuckets {
		t.Fatalf("버킷 맵 크기 = %d, evict 후 재삽입으로 %d 여야 한다", n, maxBuckets)
	}
}

// evict 는 비차단 후보가 있으면 429 차단 버킷을 제거하지 않는다(차단 정보 보존).
func TestEviction_PrefersNonBlockedBucket(t *testing.T) {
	now := offPeak()
	l := New(1.0, true).withClock(func() time.Time { return now })

	// 표본(8) 미만의 버킷만 만들어 표본이 전체를 덮게 한다.
	for _, c := range []string{"a", "b", "c", "d", "e"} {
		l.Allow(c, "MARKET_DATA")
	}
	l.Observe("c", "MARKET_DATA", 0, time.Hour) // c 만 429 차단

	l.mu.Lock()
	l.evictLocked(now)
	_, cAlive := l.buckets["c|MARKET_DATA"]
	n := len(l.buckets)
	l.mu.Unlock()

	if !cAlive {
		t.Fatal("비차단 후보가 있는데 차단 버킷(c)이 제거되었다")
	}
	if n != 4 {
		t.Fatalf("evict 후 버킷 수 = %d, want 4", n)
	}
}

package main

import (
	"context"
	"math/big"
	"sync"
	"testing"
	"time"
)

// ─────────────────────────────────────────────
// Mock 实现
// ─────────────────────────────────────────────

type mockRPC struct {
	balances    map[string]*big.Int
	blockHeight int64
}

func (m *mockRPC) GetBalance(_ context.Context, addr string) (*big.Int, error) {
	if b, ok := m.balances[addr]; ok {
		return new(big.Int).Set(b), nil
	}
	return big.NewInt(0), nil
}
func (m *mockRPC) GetBlockHeight(_ context.Context) (int64, error) {
	return m.blockHeight, nil
}

type mockRepo struct {
	addresses   map[string][]string           // chain → addresses
	balances    map[string]*big.Int            // "chain:symbol:addr" → balance
	localHeight map[string]int64               // chain → height
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		addresses:   make(map[string][]string),
		balances:    make(map[string]*big.Int),
		localHeight: make(map[string]int64),
	}
}
func (r *mockRepo) GetManagedAddresses(_ context.Context, chain string) ([]string, error) {
	return r.addresses[chain], nil
}
func (r *mockRepo) GetBalance(_ context.Context, chain, symbol, addr string) (*big.Int, error) {
	key := chain + ":" + symbol + ":" + addr
	if b, ok := r.balances[key]; ok {
		return new(big.Int).Set(b), nil
	}
	return big.NewInt(0), nil
}
func (r *mockRepo) GetLocalHeight(_ context.Context, chain string) (int64, error) {
	return r.localHeight[chain], nil
}

type mockAlarm struct {
	mu     sync.Mutex
	alerts []string // "title|msg"
}

func (a *mockAlarm) Send(_ context.Context, title, msg string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.alerts = append(a.alerts, title+"|"+msg)
	return nil
}

func (a *mockAlarm) count(title string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, s := range a.alerts {
		if len(s) >= len(title) && s[:len(title)] == title {
			n++
		}
	}
	return n
}

// ─────────────────────────────────────────────
// 测试用例
// ─────────────────────────────────────────────

// TestReconcile_Balanced 链上链下余额一致时，不应产生告警。
func TestReconcile_Balanced(t *testing.T) {
	rpc := &mockRPC{blockHeight: 1000, balances: map[string]*big.Int{
		"0xAddr1": big.NewInt(1_000_000),
		"0xAddr2": big.NewInt(2_000_000),
	}}
	repo := newMockRepo()
	repo.addresses["ETH"] = []string{"0xAddr1", "0xAddr2"}
	repo.balances["ETH:USDT:0xAddr1"] = big.NewInt(1_000_000)
	repo.balances["ETH:USDT:0xAddr2"] = big.NewInt(2_000_000)
	repo.localHeight["ETH"] = 999

	alarm := &mockAlarm{}
	r := NewReconciler("ETH", "USDT", rpc, repo, alarm)

	result, err := r.CheckOnce(context.Background())
	if err != nil {
		t.Fatalf("CheckOnce: %v", err)
	}
	if result.Diff.Sign() != 0 {
		t.Errorf("expected diff=0, got %s", result.Diff)
	}
}

// TestReconcile_Discrepancy 链上余额多出1个单位超过阈值时，应触发告警。
func TestReconcile_Discrepancy(t *testing.T) {
	// 链上余额比数据库多 50000（超过 balanceThreshold=10000）
	rpc := &mockRPC{blockHeight: 1000, balances: map[string]*big.Int{
		"0xAddr1": big.NewInt(1_050_000),
	}}
	repo := newMockRepo()
	repo.addresses["ETH"] = []string{"0xAddr1"}
	repo.balances["ETH:USDT:0xAddr1"] = big.NewInt(1_000_000)
	repo.localHeight["ETH"] = 999

	alarm := &mockAlarm{}
	r := NewReconciler("ETH", "USDT", rpc, repo, alarm)

	r.check(context.Background())

	if alarm.count("余额偏差") == 0 {
		t.Error("expected '余额偏差' alert for discrepancy > threshold")
	}
}

// TestReconcile_HeightLag 本地高度落后超过阈值时，应触发同步落后告警，不做余额对比。
func TestReconcile_HeightLag(t *testing.T) {
	rpc := &mockRPC{blockHeight: 2000} // 链上高度 2000
	repo := newMockRepo()
	repo.addresses["ETH"] = []string{"0xAddr1"}
	repo.localHeight["ETH"] = 1900 // gap = 100 > heightThreshold(50)

	alarm := &mockAlarm{}
	r := NewReconciler("ETH", "USDT", rpc, repo, alarm)

	r.check(context.Background())

	if alarm.count("同步落后") == 0 {
		t.Error("expected '同步落后' alert when height gap > 50")
	}
}

// TestReconcile_MonotonicIncrease 差值连续增大时，应触发单调性告警。
//
// 模拟场景：系统持续漏记充值
//
//	第1次对账：diff = +20000
//	第2次对账：diff = +40000（增大）→ 触发单调性告警
func TestReconcile_MonotonicIncrease(t *testing.T) {
	// 第1次：链上多 20000
	rpc := &mockRPC{blockHeight: 1000, balances: map[string]*big.Int{
		"0xAddr1": big.NewInt(1_020_000),
	}}
	repo := newMockRepo()
	repo.addresses["ETH"] = []string{"0xAddr1"}
	repo.balances["ETH:USDT:0xAddr1"] = big.NewInt(1_000_000)
	repo.localHeight["ETH"] = 999

	alarm := &mockAlarm{}
	r := NewReconciler("ETH", "USDT", rpc, repo, alarm)

	r.check(context.Background()) // diff = +20000, 无单调性告警（首次）

	// 第2次：链上多 40000（差值增大）
	rpc.balances["0xAddr1"] = big.NewInt(1_040_000)
	r.check(context.Background()) // diff = +40000 > +20000 → 单调性告警

	if alarm.count("余额差值单调增大") == 0 {
		t.Error("expected monotonic increase alert")
	}
}

// TestReconcile_MonotonicDecrease 差值连续减小时（正在恢复），不应触发单调性告警。
func TestReconcile_MonotonicDecrease(t *testing.T) {
	rpc := &mockRPC{blockHeight: 1000, balances: map[string]*big.Int{
		"0xAddr1": big.NewInt(1_040_000),
	}}
	repo := newMockRepo()
	repo.addresses["ETH"] = []string{"0xAddr1"}
	repo.balances["ETH:USDT:0xAddr1"] = big.NewInt(1_000_000)
	repo.localHeight["ETH"] = 999

	alarm := &mockAlarm{}
	r := NewReconciler("ETH", "USDT", rpc, repo, alarm)

	r.check(context.Background()) // diff = +40000

	// 差值缩小到 20000（正在恢复）
	rpc.balances["0xAddr1"] = big.NewInt(1_020_000)
	r.check(context.Background()) // diff = +20000 < +40000，差值减小

	if alarm.count("余额差值单调增大") > 0 {
		t.Error("decreasing diff should not trigger monotonic alert")
	}
}

// TestReconcile_AlertDedup 相同告警 45 分钟内只发一次。
func TestReconcile_AlertDedup(t *testing.T) {
	rpc := &mockRPC{blockHeight: 1000, balances: map[string]*big.Int{
		"0xAddr1": big.NewInt(1_050_000),
	}}
	repo := newMockRepo()
	repo.addresses["ETH"] = []string{"0xAddr1"}
	repo.balances["ETH:USDT:0xAddr1"] = big.NewInt(1_000_000)
	repo.localHeight["ETH"] = 999

	alarm := &mockAlarm{}
	r := NewReconciler("ETH", "USDT", rpc, repo, alarm)

	// 连续触发3次相同告警
	r.check(context.Background())
	r.check(context.Background())
	r.check(context.Background())

	// 由于 45 分钟去重，"余额偏差" 只应发送1次
	if n := alarm.count("余额偏差"); n != 1 {
		t.Errorf("expected 1 deduped alert, got %d", n)
	}
}

// TestReconcile_Run_ContextCancel Run() 在 ctx 取消后应正常退出。
func TestReconcile_Run_ContextCancel(t *testing.T) {
	rpc := &mockRPC{blockHeight: 1000}
	repo := newMockRepo()
	repo.localHeight["ETH"] = 999

	alarm := &mockAlarm{}
	r := NewReconciler("ETH", "USDT", rpc, repo, alarm)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := r.Run(ctx, 10*time.Millisecond)
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

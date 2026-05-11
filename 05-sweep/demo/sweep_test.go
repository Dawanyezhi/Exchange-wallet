package main

import (
	"context"
	"math/big"
	"testing"
)

// ─────────────────────────────────────────────
// 测试专用 Mock（避免与 main.go 的 mockRepo/mockRPC 冲突）
// ─────────────────────────────────────────────

type sweepTestRepo struct {
	candidates []*Address
	heights    map[string]int64
	swept      map[string]string // addr → txHash
}

func newSweepTestRepo() *sweepTestRepo {
	return &sweepTestRepo{
		heights: make(map[string]int64),
		swept:   make(map[string]string),
	}
}

func (r *sweepTestRepo) GetSweepCandidates(_ context.Context, limit int) ([]*Address, error) {
	if limit >= len(r.candidates) {
		return r.candidates, nil
	}
	return r.candidates[:limit], nil
}
func (r *sweepTestRepo) GetLastDepositHeight(_ context.Context, addr string) (int64, error) {
	return r.heights[addr], nil
}
func (r *sweepTestRepo) MarkSwept(_ context.Context, addr, txHash string) error {
	r.swept[addr] = txHash
	return nil
}

type sweepTestRPC struct {
	balances map[string]*big.Int
	gasPrice *big.Int
	sentTxs  []string
}

func newSweepTestRPC(gasPrice int64) *sweepTestRPC {
	return &sweepTestRPC{
		balances: make(map[string]*big.Int),
		gasPrice: big.NewInt(gasPrice),
	}
}

func (r *sweepTestRPC) GetBalance(_ context.Context, addr string) (*big.Int, error) {
	if b, ok := r.balances[addr]; ok {
		return new(big.Int).Set(b), nil
	}
	return big.NewInt(0), nil
}
func (r *sweepTestRPC) EstimateGas(_ context.Context, _, _ string, _ []byte) (uint64, error) {
	return 65000, nil
}
func (r *sweepTestRPC) GetGasPrice(_ context.Context) (*big.Int, error) {
	return new(big.Int).Set(r.gasPrice), nil
}
func (r *sweepTestRPC) SendTransaction(_ context.Context, tx *SweepTx) (string, error) {
	hash := "0xTx_" + tx.From
	r.sentTxs = append(r.sentTxs, hash)
	return hash, nil
}

// ─────────────────────────────────────────────
// 辅助函数
// ─────────────────────────────────────────────

func defaultSweepConfig(safeHeight int64) *SweepConfig {
	return &SweepConfig{
		Chain:       "ETH",
		HotWallet:   "0xHotWallet",
		MinBalance:  big.NewInt(1_000_000),        // 1 USDT
		KeepETH:     big.NewInt(1_000_000_000_000_000), // 0.001 ETH
		MaxGasPrice: big.NewInt(200_000_000_000),  // 200 Gwei
		SafeHeight:  safeHeight,
	}
}

// ─────────────────────────────────────────────
// 测试用例
// ─────────────────────────────────────────────

// TestSweepPipeline_Normal 正常归集：高于阈值且安全高度通过的地址应被归集。
func TestSweepPipeline_Normal(t *testing.T) {
	repo := newSweepTestRepo()
	repo.candidates = []*Address{
		{Addr: "0xUser1", UID: 1001, Symbol: "USDT", Balance: big.NewInt(100_000_000)},
	}
	repo.heights["0xUser1"] = 900 // 低于 safeHeight

	rpc := newSweepTestRPC(5_000_000_000) // 5 Gwei
	rpc.balances["0xUser1"] = big.NewInt(1e16) // 0.01 ETH (enough for gas)

	pipeline := NewPipeline(repo, rpc, defaultSweepConfig(1000))
	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if len(repo.swept) == 0 {
		t.Error("expected 0xUser1 to be swept, but MarkSwept was not called")
	}
}

// TestSweepPipeline_SafeHeightFilter 充值高度超过 SafeHeight 的地址应被跳过。
func TestSweepPipeline_SafeHeightFilter(t *testing.T) {
	repo := newSweepTestRepo()
	repo.candidates = []*Address{
		{Addr: "0xNewDeposit", UID: 1002, Symbol: "USDT", Balance: big.NewInt(50_000_000)},
	}
	repo.heights["0xNewDeposit"] = 1050 // 高于 safeHeight=1000，应跳过

	rpc := newSweepTestRPC(5_000_000_000)
	rpc.balances["0xNewDeposit"] = big.NewInt(1e16)

	pipeline := NewPipeline(repo, rpc, defaultSweepConfig(1000))
	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if _, swept := repo.swept["0xNewDeposit"]; swept {
		t.Error("address with recent deposit should not be swept (SafeHeight check)")
	}
}

// TestSweepPipeline_BelowMinBalance 余额低于最小阈值的地址应被跳过。
func TestSweepPipeline_BelowMinBalance(t *testing.T) {
	repo := newSweepTestRepo()
	repo.candidates = []*Address{
		{Addr: "0xDust", UID: 1003, Symbol: "USDT", Balance: big.NewInt(100)}, // < 1_000_000
	}
	repo.heights["0xDust"] = 500

	rpc := newSweepTestRPC(5_000_000_000)
	rpc.balances["0xDust"] = big.NewInt(1e16)

	pipeline := NewPipeline(repo, rpc, defaultSweepConfig(1000))
	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if _, swept := repo.swept["0xDust"]; swept {
		t.Error("dust address (below minBalance) should not be swept")
	}
}

// TestSweepPipeline_GasPriceTooHigh Gas Price 超限时整个归集应暂停。
func TestSweepPipeline_GasPriceTooHigh(t *testing.T) {
	repo := newSweepTestRepo()
	repo.candidates = []*Address{
		{Addr: "0xUser4", UID: 1004, Symbol: "USDT", Balance: big.NewInt(100_000_000)},
	}
	repo.heights["0xUser4"] = 500

	// Gas Price 250 Gwei > max 200 Gwei
	rpc := newSweepTestRPC(250_000_000_000)
	rpc.balances["0xUser4"] = big.NewInt(1e16)

	pipeline := NewPipeline(repo, rpc, defaultSweepConfig(1000))
	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if len(rpc.sentTxs) > 0 {
		t.Error("when gas price exceeds max, no txs should be sent")
	}
}

// TestSweepPipeline_NeedsFee ETH 不足时发补费交易，本轮不发归集（等下次循环）。
func TestSweepPipeline_NeedsFee(t *testing.T) {
	repo := newSweepTestRepo()
	repo.candidates = []*Address{
		{Addr: "0xNoGas", UID: 1005, Symbol: "USDT", Balance: big.NewInt(100_000_000)},
	}
	repo.heights["0xNoGas"] = 500

	rpc := newSweepTestRPC(5_000_000_000) // 5 Gwei
	rpc.balances["0xNoGas"] = big.NewInt(0) // 无 ETH → 触发补费

	pipeline := NewPipeline(repo, rpc, defaultSweepConfig(1000))
	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// 验证1：发送了补费交易（hotWallet → 0xNoGas）
	if len(rpc.sentTxs) == 0 {
		t.Error("expected fee tx to be sent")
	}

	// 验证2：本轮不应归集（0xNoGas 没有被 MarkSwept）
	// 生产中：等补费上链后，下一个 sweep 循环才会发归集交易
	if _, swept := repo.swept["0xNoGas"]; swept {
		t.Error("should NOT sweep in the same cycle as fee tx; must wait for fee confirmation")
	}
}

// ─────────────────────────────────────────────
// calcFeeAmount 单元测试
// ─────────────────────────────────────────────

func TestCalcFeeAmount(t *testing.T) {
	cases := []struct {
		name     string
		gweiStr  int64 // gas price in gwei
		expected int64 // expected fee amount in wei
	}{
		{"low gas (5 Gwei)", 5_000_000_000, 6_000_000_000_000_000},
		{"mid gas (30 Gwei)", 30_000_000_000, 30_000_000_000_000_000},
		{"high gas (150 Gwei)", 150_000_000_000, 120_000_000_000_000_000},
		{"very high gas (500 Gwei)", 500_000_000_000, 0}, // 暂停
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fee := calcFeeAmount(big.NewInt(tc.gweiStr))
			if fee.Int64() != tc.expected {
				t.Errorf("expected %d, got %d", tc.expected, fee.Int64())
			}
		})
	}
}

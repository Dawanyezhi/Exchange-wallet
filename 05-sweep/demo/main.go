package main

import (
	"context"
	"fmt"
	"math/big"
)

// mockRepo 内存 mock 仓库
type mockRepo struct {
	candidates []*Address
	heights    map[string]int64
}

func (r *mockRepo) GetSweepCandidates(_ context.Context, limit int) ([]*Address, error) {
	if limit > len(r.candidates) {
		return r.candidates, nil
	}
	return r.candidates[:limit], nil
}
func (r *mockRepo) GetLastDepositHeight(_ context.Context, addr string) (int64, error) {
	return r.heights[addr], nil
}
func (r *mockRepo) MarkSwept(_ context.Context, addr string, txHash string) error {
	fmt.Printf("[db] marked swept: addr=%s txHash=%s\n", addr, txHash)
	return nil
}

// mockRPC 内存 mock RPC
type mockRPC struct {
	balances map[string]*big.Int
	gasPrice *big.Int
}

func (r *mockRPC) GetBalance(_ context.Context, addr string) (*big.Int, error) {
	if b, ok := r.balances[addr]; ok {
		return new(big.Int).Set(b), nil
	}
	return big.NewInt(0), nil
}
func (r *mockRPC) EstimateGas(_ context.Context, _, _ string, _ []byte) (uint64, error) {
	return 65000, nil
}
func (r *mockRPC) GetGasPrice(_ context.Context) (*big.Int, error) {
	return new(big.Int).Set(r.gasPrice), nil
}
func (r *mockRPC) SendTransaction(_ context.Context, tx *SweepTx) (string, error) {
	return "0xSweepTx" + tx.From[:8], nil
}

func main() {
	fmt.Println("=== 05 Sweep Pipeline Demo ===")
	fmt.Println()

	safeHeight := int64(1000)

	repo := &mockRepo{
		candidates: []*Address{
			{Addr: "0xUser1111", UID: 1001, Symbol: "USDT", Balance: big.NewInt(100_000_000)},         // 100 USDT
			{Addr: "0xUser2222", UID: 1002, Symbol: "ETH", Balance: big.NewInt(500_000_000_000_000_000)}, // 0.5 ETH
			{Addr: "0xUser3333", UID: 1003, Symbol: "USDT", Balance: big.NewInt(50_000_000)},          // 50 USDT（未达SafeHeight）
		},
		heights: map[string]int64{
			"0xUser1111": 990, // 已达到 SafeHeight（990 <= 1000）
			"0xUser2222": 995, // 已达到 SafeHeight（995 <= 1000）
			"0xUser3333": 1005, // 未达到 SafeHeight！（1005 > 1000）
		},
	}

	rpc := &mockRPC{
		balances: map[string]*big.Int{
			"0xUser1111": big.NewInt(0),                      // 无 ETH，需要补费
			"0xUser2222": big.NewInt(100_000_000_000_000_000), // 0.1 ETH，够用
		},
		gasPrice: big.NewInt(5_000_000_000), // 5 Gwei
	}

	config := &SweepConfig{
		Chain:       "ETH",
		HotWallet:   "0xHotWallet",
		MinBalance:  big.NewInt(1_000_000), // 最小1 USDT (6 decimals)
		KeepETH:     big.NewInt(1_000_000_000_000_000), // 保留 0.001 ETH
		MaxGasPrice: big.NewInt(200_000_000_000),       // 最大 200 Gwei
		SafeHeight:  safeHeight,
	}

	pipeline := NewPipeline(repo, rpc, config)
	ctx := context.Background()

	fmt.Printf("SafeHeight = %d\n\n", safeHeight)
	fmt.Println("--- 开始四步归集流水线 ---")

	if err := pipeline.Run(ctx); err != nil {
		fmt.Printf("[ERROR] %v\n", err)
	}

	fmt.Println()
	fmt.Println("=== Demo 完成 ===")
	fmt.Println("关键设计：")
	fmt.Println("  1. SafeHeight 过滤：0xUser3333（高度1005>SafeHeight1000）被跳过")
	fmt.Println("  2. 0xUser1111 无ETH → 自动补费")
	fmt.Println("  3. Channel 流水线：背压控制，避免内存溢出")
}

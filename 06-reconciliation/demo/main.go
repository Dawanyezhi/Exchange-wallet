// Package main 对账系统演示程序。
// 展示：链上余额 vs 链下余额 → 差值单调性检测 → 去重告警
// 运行：go run ./06-reconciliation/demo/
package main

import (
	"context"
	"fmt"
	"math/big"
)

// ─────────────────────────────────────────────
// demo 用 Mock（不依赖真实 RPC/DB）
// ─────────────────────────────────────────────

type demoRPC struct {
	height   int64
	step     int // 模拟每次 check 后余额变化
	balances map[string]*big.Int
}

func (r *demoRPC) GetBalance(_ context.Context, addr string) (*big.Int, error) {
	if b, ok := r.balances[addr]; ok {
		return new(big.Int).Set(b), nil
	}
	return big.NewInt(0), nil
}
func (r *demoRPC) GetBlockHeight(_ context.Context) (int64, error) {
	return r.height, nil
}

type demoRepo struct {
	addrs       []string
	dbBalances  map[string]*big.Int
	localHeight int64
}

func (r *demoRepo) GetManagedAddresses(_ context.Context, _ string) ([]string, error) {
	return r.addrs, nil
}
func (r *demoRepo) GetBalance(_ context.Context, _, _, addr string) (*big.Int, error) {
	if b, ok := r.dbBalances[addr]; ok {
		return new(big.Int).Set(b), nil
	}
	return big.NewInt(0), nil
}
func (r *demoRepo) GetLocalHeight(_ context.Context, _ string) (int64, error) {
	return r.localHeight, nil
}

type printAlarm struct{}

func (a *printAlarm) Send(_ context.Context, title, msg string) error {
	fmt.Printf("  🚨 [ALARM] %s: %s\n", title, msg)
	return nil
}

// ─────────────────────────────────────────────
// main
// ─────────────────────────────────────────────

func main() {
	fmt.Println("=== 06 Reconciliation Demo ===")
	fmt.Println()
	fmt.Println("对账核心逻辑：链上余额 vs 数据库余额，追踪差值单调性")
	fmt.Println()

	addrs := []string{
		"0xUser1111111111111111111111111111111111111",
		"0xUser2222222222222222222222222222222222222",
	}

	// 初始化：链上 = 链下（平衡状态）
	rpc := &demoRPC{
		height: 1000,
		balances: map[string]*big.Int{
			addrs[0]: big.NewInt(5_000_000), // 5 USDT
			addrs[1]: big.NewInt(3_000_000), // 3 USDT
		},
	}
	repo := &demoRepo{
		addrs: addrs,
		dbBalances: map[string]*big.Int{
			addrs[0]: big.NewInt(5_000_000),
			addrs[1]: big.NewInt(3_000_000),
		},
		localHeight: 999,
	}

	alarm := &printAlarm{}
	r := NewReconciler("ETH", "USDT", rpc, repo, alarm)
	ctx := context.Background()

	// ─── 场景1：平衡状态 ───
	fmt.Println("--- 场景1：链上链下余额一致 ---")
	result, _ := r.CheckOnce(ctx)
	printResult(result)

	// ─── 场景2：链上多了 20000（约 0.02 USDT 偏差，刚超告警阈值）───
	fmt.Println("\n--- 场景2：发现余额偏差（链上多 20000 = 0.02 USDT）---")
	rpc.balances[addrs[0]] = big.NewInt(5_020_000)
	r.check(ctx)
	result, _ = r.CheckOnce(ctx)
	printResult(result)

	// ─── 场景3：偏差继续增大（单调性！）───
	fmt.Println("\n--- 场景3：偏差扩大至 40000（单调增大告警！）---")
	rpc.balances[addrs[0]] = big.NewInt(5_040_000)
	r.check(ctx)

	// ─── 场景4：同步落后 ───
	fmt.Println("\n--- 场景4：本地同步落后 100 块（数据不可靠，跳过余额对比）---")
	rpc.height = 2000 // 链上高度跳到 2000
	// repo.localHeight 还是 999，gap=1001 > 50
	r.check(ctx)

	// ─── 场景5：恢复正常 ───
	fmt.Println("\n--- 场景5：本地追上后，差值归零 ---")
	rpc.height = 1001
	rpc.balances[addrs[0]] = big.NewInt(5_000_000) // 链上修复
	r.prevDiff = nil                                 // 重置单调性（模拟节点重启）
	result, _ = r.CheckOnce(ctx)
	printResult(result)

	fmt.Println()
	fmt.Println("=== Demo 完成 ===")
	fmt.Println("关键设计：")
	fmt.Println("  1. 差值单调性：连续增大 → 持续性问题，立即告警")
	fmt.Println("  2. 高度差检查：同步落后时跳过余额对比（避免误报）")
	fmt.Println("  3. 去重告警：45 分钟内相同内容只发一次，避免告警风暴")
}

func printResult(r *ReconcileResult) {
	if r == nil {
		return
	}
	diff := new(big.Int).Set(r.Diff)
	fmt.Printf("  onChain=%s  offChain=%s  diff=%s  heightGap=%d\n",
		r.OnChainTotal, r.OffChainTotal, diff, r.HeightGap)
}

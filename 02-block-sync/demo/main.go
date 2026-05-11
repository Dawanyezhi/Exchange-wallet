// Package main 区块同步 + 重组处理演示程序。
// 展示：正常同步 → 模拟重组 → 7步原子回滚 → 从分叉点重新同步
// 运行：go run ./02-block-sync/demo/
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

type noopAlarmMain struct{}

func (n *noopAlarmMain) DeepReorgAlert(_ int64) error { return nil }

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	fmt.Println("=== 02 Block Sync + Reorg Demo ===")
	fmt.Println()

	rpc := NewMockRPC()
	repo := NewMemRepository("ETH", 3)
	repo.AddManagedAddress("0xUserAddr111111111111111111111111111111111", 1001)
	repo.AddManagedAddress("0xUserAddr222222222222222222222222222222222", 1002)

	syncer := NewSyncer("ETH", 3, rpc, repo, &noopAlarmMain{}, slog.Default())
	ctx := context.Background()

	// === Phase 1: 正常同步 5 个块 ===
	fmt.Println("--- Phase 1: 正常同步 5 个块 ---")
	mainChain := []*Block{
		MakeBlock(1, "0xAAAA", "0x0000"),
		MakeBlock(2, "0xBBBB", "0xAAAA"),
		MakeBlock(3, "0xCCCC", "0xBBBB",
			MakeTx("0xTx3A", "0xExternal", "0xUserAddr111111111111111111111111111111111", "1000000000000000000"),
		),
		MakeBlock(4, "0xDDDD", "0xCCCC"),
		MakeBlock(5, "0xEEEE", "0xDDDD",
			MakeTx("0xTx5B", "0xExternal", "0xUserAddr222222222222222222222222222222222", "500000000000000000"),
		),
	}
	for _, b := range mainChain {
		rpc.AddBlock(b)
		if err := syncer.processBlock(ctx, &b.Header); err != nil {
			fmt.Printf("[ERROR] processBlock(%d): %v\n", b.Header.Height, err)
			os.Exit(1)
		}
	}

	heights, _ := repo.GetHeights(ctx)
	fmt.Printf("\n[状态] Back=%d, Front=%d, 充值记录=%d 笔\n\n",
		heights.Back, heights.Front, len(repo.GetInbounds()))

	// === Phase 2: 模拟重组 ===
	fmt.Println("--- Phase 2: 模拟重组（Block4 和 Block5 被替换）---")
	fmt.Println("  原链: ... Block3(0xCCCC) → Block4(0xDDDD) → Block5(0xEEEE)")
	fmt.Println("  新链: ... Block3(0xCCCC) → Block4'(0xDDD2) → Block5'(0xEEE2) → Block6'(0xFFF2)")
	fmt.Println()

	// 分叉链：从 Block3 开始产生不同的 Block4'
	forkChain := []*Block{
		MakeBlock(4, "0xDDD2", "0xCCCC"), // 与原 Block4 同高度但不同 hash
		MakeBlock(5, "0xEEE2", "0xDDD2"),
		MakeBlock(6, "0xFFF2", "0xEEE2",
			MakeTx("0xTx6C", "0xExternal", "0xUserAddr111111111111111111111111111111111", "2000000000000000000"),
		),
	}
	for _, b := range forkChain {
		rpc.ReplaceBlock(b)
	}
	// 更新 latest 到 6
	rpc.AddBlock(forkChain[len(forkChain)-1])

	// === Phase 3: 处理分叉块，触发重组回滚 ===
	fmt.Println("--- Phase 3: 处理分叉块，syncer 自动检测重组并回滚 ---")
	for _, b := range forkChain {
		if err := syncer.processBlock(ctx, &b.Header); err != nil {
			fmt.Printf("[ERROR] processBlock(%d): %v\n", b.Header.Height, err)
			os.Exit(1)
		}
	}

	heights, _ = repo.GetHeights(ctx)
	inbounds := repo.GetInbounds()
	fmt.Printf("\n[状态] Back=%d, Front=%d, 充值记录=%d 笔\n", heights.Back, heights.Front, len(inbounds))

	fmt.Println("\n[充值记录]:")
	for _, inb := range inbounds {
		fmt.Printf("  - Hash=%s  To=%s  Value=%s  Status=%d\n",
			inb.Hash, inb.To[:20]+"...", inb.Value, inb.Status)
	}

	fmt.Println()
	fmt.Println("=== Demo 完成 ===")
	fmt.Println("关键验证点：")
	fmt.Println("  1. parentHash 不匹配 → 自动触发重组检测")
	fmt.Println("  2. 原链的充值记录（0xTx5B）已被7步原子回滚删除")
	fmt.Println("  3. 新链的充值记录（0xTx6C）已写入")
	fmt.Println("  4. Back/Front 指针正确更新")
}

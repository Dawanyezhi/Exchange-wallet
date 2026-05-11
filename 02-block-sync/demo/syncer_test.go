package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

const (
	testChain    = "ETH"
	testConfirms = int64(3)
	userAddr1    = "0xUser1111111111111111111111111111111111111"
	userAddr2    = "0xUser2222222222222222222222222222222222222"
	externalAddr = "0xExternal0000000000000000000000000000000"
)

func newTestSyncer(rpc RPCClient, repo *MemRepository) *Syncer {
	alarm := &noopAlarm{}
	logger := slog.Default()
	return NewSyncer(testChain, testConfirms, rpc, repo, alarm, logger)
}

// TestNormalSync 正常同步10个块，验证 Back 递增、充值记录正确写入。
func TestNormalSync(t *testing.T) {
	rpc := NewMockRPC()
	repo := NewMemRepository(testChain, testConfirms)
	repo.AddManagedAddress(userAddr1, 1001)

	// 构建10个顺序块
	blocks := []*Block{
		MakeBlock(1, "0xHash1", "0xGenesis"),
		MakeBlock(2, "0xHash2", "0xHash1"),
		MakeBlock(3, "0xHash3", "0xHash2"),
		MakeBlock(4, "0xHash4", "0xHash3"),
		MakeBlock(5, "0xHash5", "0xHash4",
			MakeTx("0xTx5", externalAddr, userAddr1, "1000000000000000000"), // 1 ETH
		),
		MakeBlock(6, "0xHash6", "0xHash5"),
		MakeBlock(7, "0xHash7", "0xHash6"),
		MakeBlock(8, "0xHash8", "0xHash7"),
		MakeBlock(9, "0xHash9", "0xHash8"),
		MakeBlock(10, "0xHash10", "0xHash9"),
	}
	for _, b := range blocks {
		rpc.AddBlock(b)
	}

	syncer := newTestSyncer(rpc, repo)
	ctx := context.Background()

	// 模拟 tick：逐块处理
	for _, b := range blocks {
		if err := syncer.processBlock(ctx, &b.Header); err != nil {
			t.Fatalf("processBlock(%d): %v", b.Header.Height, err)
		}
	}

	heights, _ := repo.GetHeights(ctx)
	if heights.Back != 10 {
		t.Errorf("Back = %d, want 10", heights.Back)
	}
	// Front = Back - confirms = 10 - 3 = 7
	if heights.Front != 7 {
		t.Errorf("Front = %d, want 7", heights.Front)
	}

	// 验证高度5的充值已写入
	inbounds := repo.GetInbounds()
	if len(inbounds) != 1 {
		t.Fatalf("inbound count = %d, want 1", len(inbounds))
	}
	if inbounds[0].Hash != "0xTx5" {
		t.Errorf("inbound.Hash = %s, want 0xTx5", inbounds[0].Hash)
	}
	// Front=7 > height=5，充值已确认
	if inbounds[0].Status != InboundStatusSuccess {
		t.Errorf("inbound.Status = %d, want %d (Success)", inbounds[0].Status, InboundStatusSuccess)
	}
}

// TestReorgDetection 模拟重组：Block2 被 Block2' 替换，充值记录应自动回滚并重写。
//
// 场景：
//   Block 1: hash=0xHash1, parent=0xGenesis
//   Block 2: hash=0xHash2, parent=0xHash1  ← 已同步（含充值 0xTxA）
//   Block 2': hash=0xHash2P, parent=0xHash1 ← 分叉块（同高度不同hash，不含该充值）
//   Block 3': hash=0xHash3P, parent=0xHash2P ← 新链延伸
//
// 期望：
//   1. 同步 Block1, Block2 成功，充值 0xTxA 写入
//   2. 遇到 Block3'，parentHash(0xHash2P) ≠ Block2.hash(0xHash2)，触发重组
//   3. 回滚 Block2（7步事务），0xTxA 充值记录删除
//   4. 接受 Block2' 和 Block3'
func TestReorgDetection(t *testing.T) {
	rpc := NewMockRPC()
	repo := NewMemRepository(testChain, testConfirms)
	repo.AddManagedAddress(userAddr1, 1001)

	ctx := context.Background()
	syncer := newTestSyncer(rpc, repo)

	// 先同步 Block1 和 Block2
	block1 := MakeBlock(1, "0xHash1", "0xGenesis")
	block2 := MakeBlock(2, "0xHash2", "0xHash1",
		MakeTx("0xTxA", externalAddr, userAddr1, "1000000000000000000"),
	)
	rpc.AddBlock(block1)
	rpc.AddBlock(block2)

	if err := syncer.processBlock(ctx, &block1.Header); err != nil {
		t.Fatalf("processBlock(1): %v", err)
	}
	if err := syncer.processBlock(ctx, &block2.Header); err != nil {
		t.Fatalf("processBlock(2): %v", err)
	}

	// 验证 Block2 的充值已记录
	inbounds := repo.GetInbounds()
	if len(inbounds) != 1 || inbounds[0].Hash != "0xTxA" {
		t.Fatalf("expected inbound 0xTxA before reorg, got %v", inbounds)
	}

	// 构造分叉块
	block2Fork := MakeBlock(2, "0xHash2P", "0xHash1") // 同高度不同 hash，不含充值
	block3Fork := MakeBlock(3, "0xHash3P", "0xHash2P")
	rpc.ReplaceBlock(block2Fork)
	rpc.AddBlock(block3Fork)

	// 处理 Block3'：parentHash 不匹配触发重组
	if err := syncer.processBlock(ctx, &block3Fork.Header); err != nil {
		t.Fatalf("processBlock(3'): %v", err)
	}

	// 验证 Block2 的充值记录已被删除
	inbounds = repo.GetInbounds()
	// 找 0xTxA（已删除）和 Block2' 中的新充值（无新充值）
	for _, inb := range inbounds {
		if inb.Hash == "0xTxA" {
			t.Error("inbound 0xTxA should have been reverted after reorg")
		}
	}

	// 验证 Back 指向新链高度3
	heights, _ := repo.GetHeights(ctx)
	if heights.Back != 3 {
		t.Errorf("Back = %d after reorg+sync, want 3", heights.Back)
	}
}

// TestDeepReorg 验证 Front==Back 时系统停止并返回 ErrDeepReorg。
func TestDeepReorg(t *testing.T) {
	rpc := NewMockRPC()
	repo := NewMemRepository(testChain, testConfirms)

	ctx := context.Background()
	syncer := newTestSyncer(rpc, repo)

	// 手动设置 Front == Back == 5（模拟深度重组已耗尽滑动窗口）
	repo.UpdateHeights(ctx, 5, 5)
	block1 := MakeBlock(1, "0xHash1", "0xGenesis")
	repo.SaveHeader(ctx, &Header{
		Chain: testChain, Height: 5, Hash: "0xHash5", Parent: "0xHash4",
		BTime: time.Now(),
	})
	repo.SaveHeader(ctx, &Header{
		Chain: testChain, Height: 1, Hash: "0xHash1", Parent: "0xGenesis",
		BTime: time.Now(),
	})
	rpc.AddBlock(block1)

	// 尝试回滚1块时触发 ErrDeepReorg
	err := syncer.rollback(ctx, 1)
	if !errors.Is(err, ErrDeepReorg) {
		t.Errorf("rollback with Front==Back: got %v, want ErrDeepReorg", err)
	}
}

// TestRollbackAtomicity 验证回滚的原子性：注入错误后数据应回到回滚前状态。
func TestRollbackAtomicity(t *testing.T) {
	rpc := NewMockRPC()
	repo := NewMemRepository(testChain, testConfirms)
	repo.AddManagedAddress(userAddr1, 1001)

	ctx := context.Background()
	syncer := newTestSyncer(rpc, repo)

	// 同步 Block1 和 Block2（Block2 含充值）
	block1 := MakeBlock(1, "0xHash1", "0xGenesis")
	block2 := MakeBlock(2, "0xHash2", "0xHash1",
		MakeTx("0xTxB", externalAddr, userAddr1, "500000000000000000"),
	)
	rpc.AddBlock(block1)
	rpc.AddBlock(block2)

	syncer.processBlock(ctx, &block1.Header)
	syncer.processBlock(ctx, &block2.Header)

	// 记录回滚前状态
	inboundsBefore := repo.GetInbounds()
	heightsBefore, _ := repo.GetHeights(ctx)

	// 在第3步（revertOutboundTx）注入错误
	repo.SetRevertErrorStep(3)

	// 尝试回滚（应该失败并回滚到错误前状态）
	err := repo.RevertBlock(ctx, 2)
	if err == nil {
		t.Fatal("expected error from injected failure at step 3")
	}

	// 验证：数据应该回到注入错误前的状态（原子性保证）
	inboundsAfter := repo.GetInbounds()
	heightsAfter, _ := repo.GetHeights(ctx)

	if len(inboundsAfter) != len(inboundsBefore) {
		t.Errorf("inbound count: before=%d, after=%d (should be equal due to atomicity)",
			len(inboundsBefore), len(inboundsAfter))
	}
	if heightsAfter.Back != heightsBefore.Back {
		t.Errorf("Back: before=%d, after=%d (should be equal due to atomicity)",
			heightsBefore.Back, heightsAfter.Back)
	}
}

type noopAlarm struct{}

func (n *noopAlarm) DeepReorgAlert(_ int64) error { return nil }

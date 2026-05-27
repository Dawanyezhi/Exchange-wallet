package main

import (
	"context"
	"fmt"
	"strings"
)

// detectReorg 检测是否发生重组。
// 返回需要回滚的块数（0 表示无重组）。
//
// 算法：比较新块的 parentHash 与本地最新块的 hash。
// 如果不匹配，说明本地最新块已经不在主链上，需要回滚。
func (s *Syncer) detectReorg(ctx context.Context, newHeader *BlockHeader) (int, error) {
	head, err := s.repo.GetHead(ctx)
	if err != nil {
		return 0, fmt.Errorf("reorg: get head: %w", err)
	}
	if head == nil {
		return 0, nil // 首次同步，没有本地状态，不需要检测
	}

	if newHeader.ParentHash == head.Hash {
		return 0, nil // 正常连接：新块的父哈希等于本地最新块哈希
	}

	// parentHash 不匹配，发生重组
	// 找到公共祖先，计算需要回滚几块
	depth, err := s.findRollbackDepth(ctx, newHeader)
	if err != nil {
		return 0, fmt.Errorf("reorg: find rollback depth: %w", err)
	}
	return depth, nil
}

// findRollbackDepth 从新块的 parentHash 向上追溯，找到本地链上的公共祖先。
// 返回需要回滚的块数（= 本地最新块高度 - 公共祖先高度）。
//
// 算法（双指针对齐）：
//  1. 从新块 parentHash 出发，在本地链上查找是否存在匹配块
//  2. 若不匹配，通过 RPC 获取分叉链上该高度的块，继续向上追溯
//  3. 直到找到公共祖先或超过最大搜索深度
func (s *Syncer) findRollbackDepth(ctx context.Context, newHeader *BlockHeader) (int, error) {
	head, err := s.repo.GetHead(ctx)
	if err != nil {
		return 0, fmt.Errorf("reorg: get head: %w", err)
	}

	// 从新块的父块开始向上追溯
	searchHash := newHeader.ParentHash
	searchHeight := newHeader.Height - 1

	for depth := 1; depth <= 100; depth++ {
		if searchHeight <= 0 {
			break
		}

		localHeader, err := s.repo.GetHeaderByHeight(ctx, searchHeight)
		if err != nil {
			return 0, fmt.Errorf("reorg: get local header at %d: %w", searchHeight, err)
		}

		if localHeader != nil && strings.EqualFold(localHeader.Hash, searchHash) {
			// 找到公共祖先，计算需要回滚的块数
			return int(head.Height - searchHeight), nil
		}

		// 公共祖先还在更深处：通过 RPC 获取分叉链上 searchHeight 的块，
		// 继续向上追溯其 parentHash
		forkBlock, err := s.rpc.GetBlockByHeight(ctx, searchHeight)
		if err != nil {
			return 0, fmt.Errorf("reorg: get fork block at %d: %w", searchHeight, err)
		}
		searchHash = forkBlock.Header.ParentHash
		searchHeight--
	}

	return 0, fmt.Errorf("reorg: cannot find common ancestor within 100 blocks")
}

// rollback 执行原子回滚，从 Back 向前逐块回滚 depth 个块。
//
// 每次 RevertBlock 调用在数据库层面是一个事务：
//
//	Step 1: revertBalance    - 回滚余额变更(通过该区块内的余额变更日志)
//	Step 2: revertInboundTx  - 删除充值记录
//	Step 3: revertOutboundTx - 提现状态退回 Pending(余额不需要修改, 钱还在热钱包地址, 重新发送上链即可)
//	Step 4: revertSystemTx   - 系统交易退回 Pending
//	Step 5: revertHeader     - 删除区块头
//	Step 6: revertHeight     - Back 减1
func (s *Syncer) rollback(ctx context.Context, depth int) error {
	// 本地已保存的最新区块
	heights, err := s.repo.GetHeights(ctx)
	if err != nil {
		return fmt.Errorf("rollback: get heights: %w", err)
	}

	for i := 0; i < depth; i++ {
		currentHeight := heights.Back - int64(i)

		if heights.Front >= heights.Back-int64(i) {
			// 深度重组！Front == Back，已超出自动回滚范围
			s.logger.Error("deep reorg detected",
				"chain", s.chain,
				"front", heights.Front,
				"back", heights.Back,
				"attempted_rollback_height", currentHeight,
			)
			if err := s.alarm.DeepReorgAlert(currentHeight); err != nil {
				s.logger.Error("failed to send deep reorg alert", "error", err)
			}
			return ErrDeepReorg
		}

		s.logger.Info("reverting block",
			"chain", s.chain,
			"height", currentHeight,
			"step", fmt.Sprintf("%d/%d", i+1, depth),
		)

		if err := s.repo.RevertBlock(ctx, currentHeight); err != nil {
			return fmt.Errorf("rollback: revert block %d: %w", currentHeight, err)
		}

		s.logger.Info("block reverted",
			"chain", s.chain,
			"height", currentHeight,
		)
	}
	return nil
}

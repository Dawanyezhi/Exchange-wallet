package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Syncer 区块同步器：轮询最新块，检测重组，处理充值。
type Syncer struct {
	chain    string
	rpc      RPCClient
	repo     Repository
	confirms int64 // 确认数（SafeHeight = latestHeight - confirms）
	alarm    Alarm
	logger   *slog.Logger
}

// NewSyncer 创建同步器。
func NewSyncer(chain string, confirms int64, rpc RPCClient, repo Repository, alarm Alarm, logger *slog.Logger) *Syncer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Syncer{
		chain:    chain,
		rpc:      rpc,
		repo:     repo,
		confirms: confirms,
		alarm:    alarm,
		logger:   logger,
	}
}

// Run 主同步循环。阻塞直到 ctx 取消。
func (s *Syncer) Run(ctx context.Context) error {
	s.logger.Info("syncer started", "chain", s.chain, "confirms", s.confirms)
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("syncer stopped", "chain", s.chain)
			return ctx.Err()
		default:
		}

		if err := s.tick(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.logger.Error("syncer tick failed", "chain", s.chain, "error", err)
			// 非致命错误：等待后重试
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}

		// 正常间隔
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// tick 单次同步轮询：获取最新块 → 与本地对比 → 处理新块。
func (s *Syncer) tick(ctx context.Context) error {
	latest, err := s.rpc.GetLatestHeader(ctx)
	if err != nil {
		return fmt.Errorf("syncer: get latest header: %w", err)
	}

	heights, err := s.repo.GetHeights(ctx)
	if err != nil {
		return fmt.Errorf("syncer: get heights: %w", err)
	}

	if heights.Back == 0 {
		// 首次启动：从当前最新块开始同步
		return s.processBlock(ctx, latest)
	}

	if latest.Height <= heights.Back {
		// 没有新块，等待
		return nil
	}

	// 按高度顺序处理新块（从 back+1 到 latest.Height）
	for h := heights.Back + 1; h <= latest.Height; h++ {
		block, err := s.rpc.GetBlockByHeight(ctx, h)
		if err != nil {
			return fmt.Errorf("syncer: get block %d: %w", h, err)
		}
		if err := s.processBlock(ctx, &block.Header); err != nil {
			return err
		}
	}
	return nil
}

// processBlock 处理单个区块头：检测重组或正常追加。
func (s *Syncer) processBlock(ctx context.Context, header *BlockHeader) error {
	// 检测是否发生重组
	depth, err := s.detectReorg(ctx, header)
	if err != nil {
		return fmt.Errorf("syncer: detect reorg at height %d: %w", header.Height, err)
	}

	if depth > 0 {
		s.logger.Warn("reorg detected",
			"chain", s.chain,
			"height", header.Height,
			"depth", depth,
			"new_parent", header.ParentHash,
		)
		if err := s.rollback(ctx, depth); err != nil {
			return fmt.Errorf("syncer: rollback %d blocks: %w", depth, err)
		}
	}

	// 保存区块头
	h := &Header{
		Chain:  s.chain,
		Height: header.Height,
		Hash:   header.Hash,
		Parent: header.ParentHash,
		BTime:  header.Time,
	}
	if err := s.repo.SaveHeader(ctx, h); err != nil {
		return fmt.Errorf("syncer: save header %d: %w", header.Height, err)
	}

	// 更新高度指针
	heights, err := s.repo.GetHeights(ctx)
	if err != nil {
		return fmt.Errorf("syncer: get heights: %w", err)
	}
	newBack := header.Height
	newFront := newBack - s.confirms
	if newFront < 0 {
		newFront = 0
	}
	if err := s.repo.UpdateHeights(ctx, newFront, newBack); err != nil {
		return fmt.Errorf("syncer: update heights: %w", err)
	}

	// 处理充值：扫描该块中的交易
	if err := s.processInbound(ctx, header); err != nil {
		return fmt.Errorf("syncer: process inbound at %d: %w", header.Height, err)
	}

	// 确认已到安全高度的充值
	if err := s.repo.ConfirmInbound(ctx, newFront); err != nil {
		return fmt.Errorf("syncer: confirm inbound: %w", err)
	}

	shortHash := header.Hash
	if len(shortHash) > 10 {
		shortHash = shortHash[:10] + "..."
	}
	s.logger.Info("block processed",
		"chain", s.chain,
		"height", header.Height,
		"hash", shortHash,
		"front", newFront,
		"back", newBack,
		"prev_back", heights.Back,
	)
	return nil
}

package main

import (
	"context"
	"fmt"
	"time"
)

// processInbound 扫描区块中的交易，识别充值并写入数据库。
func (s *Syncer) processInbound(ctx context.Context, header *BlockHeader) error {
	block, err := s.rpc.GetBlockByHeight(ctx, header.Height)
	if err != nil {
		return fmt.Errorf("inbound: get block %d: %w", header.Height, err)
	}

	// 获取受管地址列表（充值地址 → 用户ID 的映射）
	managedAddrs, err := s.repo.GetManagedAddresses(ctx)
	if err != nil {
		return fmt.Errorf("inbound: get managed addresses: %w", err)
	}

	if len(managedAddrs) == 0 || len(block.Transactions) == 0 {
		return nil
	}

	for _, tx := range block.Transactions {
		if !tx.Success {
			continue // 失败交易不处理（第1层防护：Receipt.Status 校验）
		}

		// 加入 bloom filter 快速过滤
		uid, ok := managedAddrs[tx.To]
		if !ok {
			continue // 目标地址不在受管地址列表中
		}

		// 外部地址 → 受管地址，识别为充值
		inbound := &Inbound{
			Chain:  s.chain,
			Symbol: "ETH", // demo 简化：只处理主币
			Height: header.Height,
			Hash:   tx.Hash,
			From:   tx.From,
			To:     tx.To,
			Value:  tx.Value,
			Fee:    "0",
			Status: InboundStatusPending,
			UID:    uid,
			BTime:  header.Time,
			CTime:  time.Now(),
			MTime:  time.Now(),
		}

		if err := s.repo.SaveInbound(ctx, inbound); err != nil {
			return fmt.Errorf("inbound: save tx %s: %w", tx.Hash, err)
		}

		shortHash := tx.Hash
		if len(shortHash) > 10 {
			shortHash = shortHash[:10] + "..."
		}
		s.logger.Info("inbound detected",
			"chain", s.chain,
			"hash", shortHash,
			"to", tx.To,
			"value", tx.Value,
			"uid", uid,
		)
	}
	return nil
}

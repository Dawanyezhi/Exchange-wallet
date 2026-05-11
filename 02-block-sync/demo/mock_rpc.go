package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MockRPC 模拟 RPC 节点，可以制造重组场景。
type MockRPC struct {
	mu     sync.RWMutex
	blocks map[int64]*Block // height -> block
	latest int64
}

// NewMockRPC 创建模拟 RPC。
func NewMockRPC() *MockRPC {
	return &MockRPC{
		blocks: make(map[int64]*Block),
	}
}

// AddBlock 向模拟链上添加一个块。
func (m *MockRPC) AddBlock(block *Block) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blocks[block.Header.Height] = block
	if block.Header.Height > m.latest {
		m.latest = block.Header.Height
	}
}

// ReplaceBlock 替换某高度的块（模拟重组：同高度不同 hash）。
func (m *MockRPC) ReplaceBlock(block *Block) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blocks[block.Header.Height] = block
	// 同时替换所有更高高度的块（更新它们的 parentHash）
}

// GetLatestHeader 返回当前最新块头。
func (m *MockRPC) GetLatestHeader(_ context.Context) (*BlockHeader, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	block, ok := m.blocks[m.latest]
	if !ok {
		return nil, fmt.Errorf("mock rpc: no blocks")
	}
	h := block.Header
	return &h, nil
}

// GetBlockByHeight 按高度返回块。
func (m *MockRPC) GetBlockByHeight(_ context.Context, height int64) (*Block, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	block, ok := m.blocks[height]
	if !ok {
		return nil, fmt.Errorf("mock rpc: block %d not found", height)
	}
	return block, nil
}

// GetTransactionReceipt 返回模拟收据。
func (m *MockRPC) GetTransactionReceipt(_ context.Context, txHash string) (*Receipt, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, block := range m.blocks {
		for _, tx := range block.Transactions {
			if tx.Hash == txHash {
				return &Receipt{
					TxHash:    txHash,
					BlockHash: block.Header.Hash,
					Status:    1,
				}, nil
			}
		}
	}
	return nil, fmt.Errorf("mock rpc: receipt for %s not found", txHash)
}

// --- 测试场景构造辅助函数 ---

// MakeBlock 构造一个块（简化版）。
func MakeBlock(height int64, hash, parentHash string, txs ...*Transaction) *Block {
	return &Block{
		Header: BlockHeader{
			Height:     height,
			Hash:       hash,
			ParentHash: parentHash,
			Time:       time.Now(),
		},
		Transactions: txs,
	}
}

// MakeTx 构造一笔充值交易。
func MakeTx(hash, from, to, value string) *Transaction {
	return &Transaction{
		Hash:    hash,
		From:    from,
		To:      to,
		Value:   value,
		Success: true,
	}
}

// Package main 提现流程 demo：Nonce 管理 + EIP-155 交易构建 + 签名验证
package main

import (
	"context"
	"fmt"
	"sync"
)

// RPCClient 简化的 RPC 接口（仅用于 Nonce 管理）。
type RPCClient interface {
	GetTransactionCount(ctx context.Context, address string, tag string) (uint64, error)
	SendRawTransaction(ctx context.Context, rawTx string) (string, error)
}

// NonceManager 串行化 Nonce 分配，防止 Nonce 冲突导致交易堵塞。
// 并发安全：使用 mutex 保证 Nonce 递增的原子性。
type NonceManager struct {
	mu      sync.Mutex
	address string
	nonce   uint64
	synced  bool
	rpc     RPCClient
}

// NewNonceManager 创建 Nonce 管理器。
// 首次调用 Next() 时会自动从 RPC 同步。
func NewNonceManager(address string, rpc RPCClient) *NonceManager {
	return &NonceManager{
		address: address,
		rpc:     rpc,
	}
}

// Next 分配下一个 Nonce，线程安全。
// 首次调用会从 RPC 同步当前 pending nonce。
func (nm *NonceManager) Next(ctx context.Context) (uint64, error) {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	if !nm.synced {
		if err := nm.syncLocked(ctx); err != nil {
			return 0, fmt.Errorf("nonce: initial sync: %w", err)
		}
	}

	n := nm.nonce
	nm.nonce++
	return n, nil
}

// Sync 从链上重新同步 Nonce（节点重启后或 Nonce 卡住时调用）。
func (nm *NonceManager) Sync(ctx context.Context) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	return nm.syncLocked(ctx)
}

// Peek 返回当前缓存的 Nonce（不递增），用于监控。
func (nm *NonceManager) Peek() uint64 {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	return nm.nonce
}

func (nm *NonceManager) syncLocked(ctx context.Context) error {
	// "pending" 包含已广播但尚未上链的交易，是发送下一笔交易应该使用的 Nonce
	count, err := nm.rpc.GetTransactionCount(ctx, nm.address, "pending")
	if err != nil {
		return fmt.Errorf("nonce: get transaction count: %w", err)
	}
	nm.nonce = count
	nm.synced = true
	return nil
}

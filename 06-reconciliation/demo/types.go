// Package main 对账系统 demo：差值单调性检测 + 去重告警
package main

import (
	"context"
	"math/big"
	"time"
)

// ─────────────────────────────────────────────
// 接口定义
// ─────────────────────────────────────────────

// RPCClient 用于查询链上余额。
type RPCClient interface {
	// GetBalance 获取指定地址的链上余额（单位: 最小计量单位）。
	GetBalance(ctx context.Context, addr string) (*big.Int, error)
	// GetBlockHeight 获取当前链最新高度。
	GetBlockHeight(ctx context.Context) (int64, error)
}

// Repository 链下数据库接口。
type Repository interface {
	// GetManagedAddresses 返回系统管理的所有充值地址。
	GetManagedAddresses(ctx context.Context, chain string) ([]string, error)
	// GetBalance 获取某地址在数据库中记录的余额。
	GetBalance(ctx context.Context, chain, symbol, address string) (*big.Int, error)
	// GetLocalHeight 获取本地同步到的最新区块高度。
	GetLocalHeight(ctx context.Context, chain string) (int64, error)
}

// Alarm 告警通知接口（irwallet 模式：与具体告警渠道解耦）。
type Alarm interface {
	// Send 发送告警，title 用于分类，msg 是详细内容。
	Send(ctx context.Context, title, msg string) error
}

// ─────────────────────────────────────────────
// 数据结构
// ─────────────────────────────────────────────

// ReconcileResult 单次对账结果快照。
type ReconcileResult struct {
	Chain         string
	Symbol        string
	OnChainTotal  *big.Int // 链上各地址余额之和
	OffChainTotal *big.Int // 数据库中各地址余额之和
	Diff          *big.Int // OnChain - OffChain（正数=链上多，负数=链下多）
	OnChainHeight int64
	LocalHeight   int64
	HeightGap     int64 // 链上高度与本地高度差
	CheckedAt     time.Time
}

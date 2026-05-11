// Package main 区块同步与重组处理 demo
package main

import (
	"context"
	"errors"
	"time"
)

// --- 链上数据结构 ---

// BlockHeader 区块头（从 RPC 获取）。
type BlockHeader struct {
	Height     int64
	Hash       string
	ParentHash string
	Time       time.Time
}

// Block 完整区块（含交易列表）。
type Block struct {
	Header       BlockHeader
	Transactions []*Transaction
}

// Transaction 区块中的交易。
type Transaction struct {
	Hash    string
	From    string
	To      string
	Value   string // big.Int string
	Success bool
}

// Receipt 交易收据。
type Receipt struct {
	TxHash    string
	BlockHash string
	Status    uint64 // 1=success, 0=failed
	Logs      []*Log
}

// Log 事件日志（用于 ERC20 充值检测）。
type Log struct {
	Address string
	Topics  []string
	Data    string
	Removed bool // true 表示重组期间被撤销
}

// --- 数据库模型 ---

// Header 数据库中存储的区块头记录。
type Header struct {
	Chain  string    `db:"chain"`
	Height int64     `db:"height"`
	Hash   string    `db:"hash"`
	Parent string    `db:"parent"`
	BTime  time.Time `db:"btime"`
}

// Height 高度指针（Front/Back）。
type Height struct {
	Chain string    `db:"chain"`
	Front int64     `db:"front"`
	Back  int64     `db:"back"`
	CTime time.Time `db:"ctime"`
	MTime time.Time `db:"mtime"`
}

// Inbound 充值记录。
type Inbound struct {
	ID     int64     `db:"id"`
	Chain  string    `db:"chain"`
	Symbol string    `db:"symbol"`
	Height int64     `db:"height"`
	Hash   string    `db:"hash"`
	From   string    `db:"from"`
	To     string    `db:"to"`
	Value  string    `db:"value"`
	Fee    string    `db:"fee"`
	Status int       `db:"status"` // 0=Pending 1=Success 4=Revert
	UID    int64     `db:"uid"`
	BTime  time.Time `db:"btime"`
	CTime  time.Time `db:"ctime"`
	MTime  time.Time `db:"mtime"`
}

const (
	InboundStatusPending = 0
	InboundStatusSuccess = 1
	InboundStatusRevert  = 4
)

// --- 接口定义 ---

// RPCClient 区块链 RPC 接口（可 mock）。
type RPCClient interface {
	GetLatestHeader(ctx context.Context) (*BlockHeader, error)
	GetBlockByHeight(ctx context.Context, height int64) (*Block, error)
	GetTransactionReceipt(ctx context.Context, txHash string) (*Receipt, error)
}

// Repository 数据库操作接口（可 mock）。
type Repository interface {
	// 高度管理
	GetHeights(ctx context.Context) (*Height, error)
	UpdateHeights(ctx context.Context, front, back int64) error

	// 区块头管理
	GetHead(ctx context.Context) (*Header, error)
	GetHeaderByHeight(ctx context.Context, height int64) (*Header, error)
	SaveHeader(ctx context.Context, h *Header) error

	// 7步原子回滚（在一个数据库事务中执行全部7步）
	RevertBlock(ctx context.Context, height int64) error

	// 充值管理
	GetManagedAddresses(ctx context.Context) (map[string]int64, error) // address -> uid
	SaveInbound(ctx context.Context, tx *Inbound) error
	ConfirmInbound(ctx context.Context, height int64) error
}

// Alarm 告警接口（可 mock）。
type Alarm interface {
	DeepReorgAlert(height int64) error
}

// --- 错误定义 ---

// ErrDeepReorg 深度重组：Front == Back，无法继续自动回滚。
var ErrDeepReorg = errors.New("syncer: deep reorg detected, manual intervention required")

// ErrNotFound 资源不存在。
var ErrNotFound = errors.New("syncer: not found")

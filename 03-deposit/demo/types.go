// Package main 充值7层防护 demo
package main

import (
	"context"
	"math/big"
)

// RawTransaction 充值处理流水线的输入：一笔待检测的交易。
type RawTransaction struct {
	Hash        string
	From        string
	To          string
	Value       *big.Int // 原生代币金额（ETH/BNB等）
	BlockHash   string
	BlockHeight int64
	Receipt     *Receipt
	Traces      []*Trace // 内部调用追踪（可选，主币合约转账时填充，来自 debug_traceTransaction）
}

// Receipt 交易收据。
type Receipt struct {
	TxHash    string
	BlockHash string
	Status    uint64 // 1=success, 0=failed
	Logs      []*Log
}

// Log 事件日志。
type Log struct {
	Address string   // 发出事件的合约地址
	Topics  []string // Topics[0]=事件签名, Topics[1]=from, Topics[2]=to
	Data    string   // hex，Transfer 事件中是金额（uint256）
	Removed bool     // true 表示重组期间被撤销
}

// Trace 内部交易追踪（来自 debug_traceTransaction）。
type Trace struct {
	CallType string   // "call" / "delegatecall" / "staticcall"
	From     string
	To       string
	Value    string // hex
	Depth    int
	Error    string // 非空表示此调用失败
}

// Deposit 通过7层过滤后的有效充值记录。
type Deposit struct {
	TxHash  string
	Symbol  string
	From    string
	To      string
	Amount  *big.Int
	UID     int64
	IsMemo  bool // Tag 型链是否包含有效 Memo
}

// TokenConfig 白名单代币配置。
type TokenConfig struct {
	Symbol   string
	Decimals int
	Address  string
}

// Config 充值流水线配置。
type Config struct {
	Chain          string
	ManagedAddrs   map[string]int64     // address → uid
	HotAddrs       map[string]struct{}  // 热钱包地址集合
	Whitelist      map[string]TokenConfig // contract address → token config
	VerifyFailPolicy string // "ignore" or "alert_and_accept"
}

// RPCClient 用于第7层二次校验。
type RPCClient interface {
	GetTransactionReceipt(ctx context.Context, txHash string) (*Receipt, error)
}

// DepositFilter 单层过滤器接口。
// 返回 (false, nil)：过滤掉（不是充值）
// 返回 (true, nil)：通过本层
// 返回 (_, err)：处理错误
type DepositFilter interface {
	Filter(ctx context.Context, tx *RawTransaction) (bool, error)
}

package main

import (
	"context"
	"fmt"
	"strings"
)

// DepositPipeline 按顺序串联 7 层过滤器。
// 设计原则：每层独立，任何一层 false 即丢弃，不进入下一层。
type DepositPipeline struct {
	filters []DepositFilter
	config  *Config
}

// NewPipeline 构建完整的 7 层充值过滤流水线。
func NewPipeline(config *Config, rpc RPCClient) *DepositPipeline {
	return &DepositPipeline{
		config: config,
		filters: []DepositFilter{
			NewLayer1ReceiptFilter(),
			NewLayer2EventLogFilter(),
			NewLayer3WhitelistFilter(config.Whitelist),
			NewLayer4BlockHashFilter(),
			NewLayer5TraceFilter(),
			NewLayer6ClassifierFilter(config.ManagedAddrs, config.HotAddrs),
			NewLayer7VerifyFilter(rpc, config.VerifyFailPolicy),
		},
	}
}

// Process 对一笔交易执行完整的 7 层过滤。
// 返回 *Deposit（通过所有层）或 nil（被某层过滤掉）。
func (p *DepositPipeline) Process(ctx context.Context, tx *RawTransaction) (*Deposit, error) {
	for i, filter := range p.filters {
		pass, err := filter.Filter(ctx, tx)
		if err != nil {
			return nil, fmt.Errorf("deposit pipeline layer %d: %w", i+1, err)
		}
		if !pass {
			return nil, nil // 被该层过滤掉
		}
	}

	// 通过所有层，构建充值记录
	return buildDeposit(tx, p.config), nil
}

// buildDeposit 从通过验证的交易构建充值记录。
// 区分主币充值（tx.Value）和 ERC20 充值（Log.Data）。
func buildDeposit(tx *RawTransaction, config *Config) *Deposit {
	// 优先从 Transfer 事件日志提取 ERC20 充值信息
	if tx.Receipt != nil {
		for _, log := range tx.Receipt.Logs {
			if log.Removed || len(log.Topics) != 3 {
				continue
			}
			if !strings.EqualFold(log.Topics[0], transferEventSig) {
				continue
			}
			amount := parseHexBigInt(log.Data)
			if amount == nil || amount.Sign() <= 0 {
				continue
			}
			tokenCfg, ok := config.Whitelist[strings.ToLower(log.Address)]
			if !ok {
				continue
			}
			// Topics[2] = "0x000...000<20-byte-addr>"，取后 40 hex 字符
			return &Deposit{
				TxHash: tx.Hash,
				Symbol: tokenCfg.Symbol,
				From:   topic2Address(log.Topics[1]),
				To:     topic2Address(log.Topics[2]),
				Amount: amount,
			}
		}
	}

	// 原生代币充值（ETH/BNB等）
	return &Deposit{
		TxHash: tx.Hash,
		Symbol: "ETH",
		From:   tx.From,
		To:     tx.To,
		Amount: tx.Value,
	}
}

// topic2Address 将 ABI 编码的 Topic（32字节，左补零）提取为标准以太坊地址。
func topic2Address(topic string) string {
	topic = strings.TrimPrefix(topic, "0x")
	topic = strings.TrimPrefix(topic, "0X")
	if len(topic) >= 40 {
		return "0x" + topic[len(topic)-40:]
	}
	return "0x" + topic
}

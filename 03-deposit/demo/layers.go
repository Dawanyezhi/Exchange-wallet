package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// transferEventSig ERC20 Transfer 事件的 keccak256 签名哈希。
// keccak256("Transfer(address,address,uint256)")
const transferEventSig = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

// =============================================================================
// 第1层：Receipt.Status 校验
// =============================================================================

type layer1ReceiptFilter struct{}

func NewLayer1ReceiptFilter() DepositFilter { return &layer1ReceiptFilter{} }

func (f *layer1ReceiptFilter) Filter(_ context.Context, tx *RawTransaction) (bool, error) {
	if tx.Receipt == nil {
		return false, nil
	}
	// status=0 是失败交易（合约 revert），绝对不能上账
	// 失败交易仍然消耗 Gas，但不产生任何状态变更
	if tx.Receipt.Status != 1 {
		return false, nil
	}
	return true, nil
}

// =============================================================================
// 第2层：Transfer 事件日志校验
// =============================================================================

type layer2EventLogFilter struct{}

func NewLayer2EventLogFilter() DepositFilter { return &layer2EventLogFilter{} }

func (f *layer2EventLogFilter) Filter(_ context.Context, tx *RawTransaction) (bool, error) {
	if tx.Receipt == nil {
		return false, nil
	}
	for _, log := range tx.Receipt.Logs {
		// 重组期间被撤销的日志，必须过滤
		if log.Removed {
			continue
		}
		// Topics 必须恰好3个：[事件签名, from地址(32字节), to地址(32字节)]
		if len(log.Topics) != 3 {
			continue
		}
		// 第一个 Topic 必须是 Transfer 事件签名
		if !strings.EqualFold(log.Topics[0], transferEventSig) {
			continue
		}
		// 金额来自 Data（非 indexed 参数，32字节 padded uint256）
		if len(log.Data) < 2 {
			continue
		}
		amount := parseHexBigInt(log.Data)
		if amount == nil || amount.Sign() <= 0 {
			continue
		}
		// 至少找到一个有效的 Transfer 事件
		return true, nil
	}
	// 没有找到有效的 Transfer 事件
	return false, nil
}

// =============================================================================
// 第3层：Token 合约地址白名单
// =============================================================================

type layer3WhitelistFilter struct {
	whitelist map[string]TokenConfig // 小写地址 → 配置
}

func NewLayer3WhitelistFilter(whitelist map[string]TokenConfig) DepositFilter {
	normalized := make(map[string]TokenConfig, len(whitelist))
	for addr, cfg := range whitelist {
		normalized[strings.ToLower(addr)] = cfg
	}
	return &layer3WhitelistFilter{whitelist: normalized}
}

func (f *layer3WhitelistFilter) Filter(_ context.Context, tx *RawTransaction) (bool, error) {
	if tx.Receipt == nil {
		return false, nil
	}
	for _, log := range tx.Receipt.Logs {
		if log.Removed {
			continue
		}
		// 必须精确匹配合约地址（不能只匹配 symbol 字符串）
		if _, ok := f.whitelist[strings.ToLower(log.Address)]; ok {
			return true, nil
		}
	}
	return false, nil
}

// =============================================================================
// 第4层：BlockHash 一致性
// =============================================================================

type layer4BlockHashFilter struct{}

func NewLayer4BlockHashFilter() DepositFilter { return &layer4BlockHashFilter{} }

func (f *layer4BlockHashFilter) Filter(_ context.Context, tx *RawTransaction) (bool, error) {
	if tx.Receipt == nil {
		return false, nil
	}
	// 对比 receipt.BlockHash 和当前处理的 block.Hash
	// 不一致说明该交易在重组期间被重新打包到了其他块
	if tx.BlockHash != "" && tx.Receipt.BlockHash != "" {
		if !strings.EqualFold(tx.BlockHash, tx.Receipt.BlockHash) {
			return false, nil
		}
	}
	return true, nil
}

// =============================================================================
// 第5层：内部交易 Trace 验证
// =============================================================================

type layer5TraceFilter struct{}

func NewLayer5TraceFilter() DepositFilter { return &layer5TraceFilter{} }

func (f *layer5TraceFilter) Filter(_ context.Context, tx *RawTransaction) (bool, error) {
	// 如果没有 Trace 数据（普通 EOA 转账），直接通过
	// 只有通过合约触发的主币转入才会携带 Traces
	if len(tx.Traces) == 0 {
		return true, nil
	}
	// 主调用（depth=0）失败 → 整个交易回滚，不能上账
	for _, trace := range tx.Traces {
		if trace.Depth == 0 && trace.Error != "" {
			return false, nil
		}
	}
	return true, nil
}

// FilterWithTraces 带 Trace 数据的过滤（实际充值处理调用）。
func FilterWithTraces(traces []*Trace, managedAddrs map[string]int64) []*Deposit {
	var deposits []*Deposit
	for _, trace := range traces {
		// 必须是 call 类型（排除 delegatecall/staticcall）
		if trace.CallType != "call" {
			continue
		}
		// 必须有转账金额
		if trace.Value == "" || trace.Value == "0x0" || trace.Value == "0x" {
			continue
		}
		// 主调用（depth=0）失败 → 整个交易作废
		if trace.Depth == 0 && trace.Error != "" {
			return nil
		}
		// 子调用失败 → 只跳过该子树
		if trace.Depth > 0 && trace.Error != "" {
			continue
		}
		uid, ok := managedAddrs[strings.ToLower(trace.To)]
		if !ok {
			continue
		}
		amount := parseHexBigInt(trace.Value)
		if amount == nil || amount.Sign() <= 0 {
			continue
		}
		deposits = append(deposits, &Deposit{
			From:   trace.From,
			To:     trace.To,
			Amount: amount,
			UID:    uid,
		})
	}
	return deposits
}

// =============================================================================
// 第6层：交易方向分类过滤
// =============================================================================

type layer6ClassifierFilter struct {
	managedAddrs map[string]int64    // address → uid（用户充值地址）
	hotAddrs     map[string]struct{} // 热钱包地址
}

func NewLayer6ClassifierFilter(managedAddrs map[string]int64, hotAddrs map[string]struct{}) DepositFilter {
	normalized := make(map[string]int64, len(managedAddrs))
	for addr, uid := range managedAddrs {
		normalized[strings.ToLower(addr)] = uid
	}
	normalizedHot := make(map[string]struct{}, len(hotAddrs))
	for addr := range hotAddrs {
		normalizedHot[strings.ToLower(addr)] = struct{}{}
	}
	return &layer6ClassifierFilter{
		managedAddrs: normalized,
		hotAddrs:     normalizedHot,
	}
}

func (f *layer6ClassifierFilter) Filter(_ context.Context, tx *RawTransaction) (bool, error) {
	fromLower := strings.ToLower(tx.From)
	toLower := strings.ToLower(tx.To)

	// 发送方是热钱包 → 这是提现交易，不能当充值
	if _, isHot := f.hotAddrs[fromLower]; isHot {
		return false, nil
	}

	// 接收方不是受管地址 → 不是充值
	if _, isManaged := f.managedAddrs[toLower]; !isManaged {
		return false, nil
	}

	// Unknown → User：才是真正的充值
	return true, nil
}

// =============================================================================
// 第7层：二次校验
// =============================================================================

type layer7VerifyFilter struct {
	rpc              RPCClient
	verifyFailPolicy string // "ignore" or "alert_and_accept"
}

func NewLayer7VerifyFilter(rpc RPCClient, policy string) DepositFilter {
	if policy == "" {
		policy = "ignore"
	}
	return &layer7VerifyFilter{rpc: rpc, verifyFailPolicy: policy}
}

func (f *layer7VerifyFilter) Filter(ctx context.Context, tx *RawTransaction) (bool, error) {
	if f.rpc == nil {
		return true, nil // 没有二次校验 RPC，跳过
	}

	// 从 RPC 重新独立获取交易收据
	receipt2, err := f.rpc.GetTransactionReceipt(ctx, tx.Hash)
	if err != nil {
		return f.handleFailure(fmt.Sprintf("second verify: get receipt: %v", err))
	}

	// 验证 status 一致
	if receipt2.Status != tx.Receipt.Status {
		return f.handleFailure("second verify: status mismatch")
	}

	// 验证 blockHash 一致
	if !strings.EqualFold(receipt2.BlockHash, tx.Receipt.BlockHash) {
		return f.handleFailure("second verify: blockHash mismatch")
	}

	return true, nil
}

func (f *layer7VerifyFilter) handleFailure(reason string) (bool, error) {
	switch f.verifyFailPolicy {
	case "alert_and_accept":
		// 发告警但仍然上账（激进策略）
		fmt.Printf("[ALARM] 充值二次验证失败但已接受: %s\n", reason)
		return true, nil
	default: // "ignore"
		// 不上账 + 发告警（保守策略）
		fmt.Printf("[ALARM] 充值二次验证失败，已忽略: %s\n", reason)
		return false, nil
	}
}

// =============================================================================
// 辅助函数
// =============================================================================

// parseHexBigInt 解析 hex 字符串为 big.Int。
func parseHexBigInt(s string) *big.Int {
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if s == "" {
		return nil
	}
	// hex.DecodeString 要求偶数长度
	if len(s)%2 != 0 {
		s = "0" + s
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return new(big.Int).SetBytes(b)
}

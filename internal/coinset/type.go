// Package coinset 定义链类型和特性开关。
package coinset

// ChainType 区分三种主要的区块链账本模型。
type ChainType int

const (
	// Account 账户余额型：ETH/BSC/Polygon 等，地址复用，Nonce 防重放。
	Account ChainType = iota
	// UTXO 未花费输出型：BTC/LTC，每笔交易消耗 UTXO，无 Nonce。
	UTXO
	// Tag 标签型：XRP/XLM，一个地址+Memo 区分用户，Memo 丢失资产找回困难。
	Tag
)

func (c ChainType) String() string {
	switch c {
	case Account:
		return "Account"
	case UTXO:
		return "UTXO"
	case Tag:
		return "Tag"
	default:
		return "Unknown"
	}
}

// FeatureGate 特性开关，用于运行时控制链特定行为。
// 使用位掩码，可组合多个特性：features = FeatureLayer2 | FeatureEIP1559
type FeatureGate uint64

const (
	// FeatureLayer2 标记为 L2 链（确认数按链独立配置，非统一 SafeHeight）。
	FeatureLayer2 FeatureGate = 1 << iota
	// FeatureInternalTx 支持内部交易追踪（debug_traceTransaction）。
	FeatureInternalTx
	// FeatureEIP1559 支持 EIP-1559 费用模型（maxFeePerGas / maxPriorityFeePerGas）。
	FeatureEIP1559
	// FeatureFeeToken 有燃烧费代币（如 SafeMoon），归集时需要特殊处理 burn fee。
	FeatureFeeToken
)

// Has 检查是否包含指定特性。
func (f FeatureGate) Has(feature FeatureGate) bool {
	return f&feature != 0
}

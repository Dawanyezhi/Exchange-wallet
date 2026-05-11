package coinset

// Chain 描述一条链的静态配置。
type Chain struct {
	// Name 链的唯一标识，如 "ETH"、"BSC"、"POLYGON"。
	Name string
	// ChainID EVM 链 ID，用于 EIP-155 签名防重放。0 表示非 EVM 链。
	ChainID int64
	// Type 账本模型类型。
	Type ChainType
	// Confirms 充值所需确认数（SafeHeight = 当前高度 - Confirms）。
	// 确认数设计原则：
	//   ETH:     12（PoS 后降低，最终性约12块）
	//   BSC:     20（3秒出块，20块约1分钟）
	//   Polygon: 400（历史上发生过高度25280775的深度分叉）
	//   ETC:     500（51%攻击高风险链）
	//   L2链:   差异大，从1（Metis）到5000（zkSync）不等
	Confirms int64
	// Features 该链支持的特性集合（位掩码）。
	Features FeatureGate
	// NativeSymbol 原生代币符号，如 "ETH"、"BNB"。
	NativeSymbol string
	// Decimals 原生代币精度，如 18。
	Decimals int
}

// 预定义的常用链配置
var (
	ETH = Chain{
		Name:         "ETH",
		ChainID:      1,
		Type:         Account,
		Confirms:     12,
		Features:     FeatureInternalTx | FeatureEIP1559,
		NativeSymbol: "ETH",
		Decimals:     18,
	}

	BSC = Chain{
		Name:         "BSC",
		ChainID:      56,
		Type:         Account,
		Confirms:     20,
		Features:     FeatureInternalTx | FeatureEIP1559,
		NativeSymbol: "BNB",
		Decimals:     18,
	}

	Polygon = Chain{
		Name:         "POLYGON",
		ChainID:      137,
		Type:         Account,
		// 400确认：历史上Polygon曾在高度25280775附近发生深度分叉，持续约一小时
		// 详见：https://forum.polygon.technology/t/matic-network-chain-reorganization-post-mortem/
		Confirms:     400,
		Features:     FeatureInternalTx | FeatureEIP1559,
		NativeSymbol: "MATIC",
		Decimals:     18,
	}

	ETC = Chain{
		Name:         "ETC",
		ChainID:      61,
		Type:         Account,
		// 500确认：ETC历史上多次遭受51%攻击，2020年8月三次攻击中最深达3693块
		Confirms:     500,
		Features:     FeatureInternalTx,
		NativeSymbol: "ETC",
		Decimals:     18,
	}

	// Metis 是L2链，Sequencer提交到L1后基本不回滚，可设1
	Metis = Chain{
		Name:         "METIS",
		ChainID:      1088,
		Type:         Account,
		Confirms:     1,
		Features:     FeatureLayer2 | FeatureEIP1559,
		NativeSymbol: "METIS",
		Decimals:     18,
	}
)

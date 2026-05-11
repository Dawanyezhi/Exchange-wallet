// Package config 钱包服务配置结构体。
package config

// WalletConfig 钱包主服务配置。
type WalletConfig struct {
	// Chain 链相关配置
	Chain ChainConfig `yaml:"chain"`
	// DB 数据库连接
	DB DBConfig `yaml:"db"`
	// RPC 节点连接
	RPC RPCConfig `yaml:"rpc"`
	// KeyMan 密钥管理服务配置
	KeyMan KeyManConfig `yaml:"keyman"`
	// Alarm 告警配置
	Alarm AlarmConfig `yaml:"alarm"`
	// Sweep 归集配置
	Sweep SweepConfig `yaml:"sweep"`
}

// ChainConfig 链的运行时配置。
type ChainConfig struct {
	Name         string   `yaml:"name"`          // 链标识，如 "ETH"
	ChainID      int64    `yaml:"chain_id"`       // EVM 链 ID
	Confirms     int64    `yaml:"confirms"`       // 充值确认数
	Tokens       []Token  `yaml:"tokens"`         // 支持的 ERC20 代币列表
	HotWallet    string   `yaml:"hot_wallet"`     // 热钱包地址
	MaxGasGwei   int64    `yaml:"max_gas_gwei"`   // Gas Price 上限（Gwei）
	KeepETHWei   string   `yaml:"keep_eth_wei"`   // 归集时保留的最小主币（wei）
}

// Token ERC20 代币配置。
type Token struct {
	Symbol   string `yaml:"symbol"`   // 代币符号
	Address  string `yaml:"address"`  // 合约地址（白名单的核心）
	Decimals int    `yaml:"decimals"` // 精度
	MinSweep string `yaml:"min_sweep"` // 最小归集金额
}

// DBConfig 数据库配置。
type DBConfig struct {
	DSN         string `yaml:"dsn"`          // MySQL DSN
	MaxOpenConn int    `yaml:"max_open_conn"` // 最大连接数
	MaxIdleConn int    `yaml:"max_idle_conn"` // 最大空闲连接数
}

// RPCConfig 区块链节点配置。
type RPCConfig struct {
	Endpoints   []string `yaml:"endpoints"`    // RPC 节点列表（支持多节点 failover）
	Timeout     int      `yaml:"timeout_sec"`  // 请求超时（秒）
	BackupEndpoint string `yaml:"backup_endpoint"` // 备用节点（用于二次校验）
}

// KeyManConfig 密钥管理服务配置。
type KeyManConfig struct {
	Endpoint string `yaml:"endpoint"` // walletmanager HTTP 地址
	PubKey   string `yaml:"pubkey"`   // X25519 公钥（用于加密传输）
}

// AlarmConfig 告警配置。
type AlarmConfig struct {
	LarkWebhook string `yaml:"lark_webhook"` // Lark Webhook URL
}

// SweepConfig 归集配置。
type SweepConfig struct {
	IntervalSec int    `yaml:"interval_sec"` // 归集间隔（秒）
	MinBalance  string `yaml:"min_balance"`   // 最小归集余额（wei）
}

// KeyManServerConfig 密钥管理服务配置。
type KeyManServerConfig struct {
	ListenAddr     string   `yaml:"listen_addr"`      // HTTP 监听地址
	KeystorePath   string   `yaml:"keystore_path"`    // Keystore 文件路径
	PrivKeyX25519  string   `yaml:"priv_key_x25519"`  // X25519 私钥（传输加密）
	AllowedChains  []string `yaml:"allowed_chains"`   // 允许派生的链列表
}

# BTC 链接入调研

本文按交易所托管钱包视角调研 BTC 接入：离线地址生成、离线签名、区块同步、UTXO 状态管理、充值提现归集、RPC 能力、节点部署、费用模型、重组处理和数据库设计。

BTC 与当前项目已有 EVM demo 的最大差异：BTC 是 UTXO 模型，没有账户 Nonce，没有链上 `address -> balance` 状态查询语义；交易所钱包必须通过自建扫块索引维护托管地址相关 UTXO 集，并在重组时恢复被回滚交易花费的 UTXO。

## 1. 基础结论

| 维度 | BTC 结论 | 对钱包工程的影响 |
|------|----------|------------------|
| 账本模型 | UTXO | 充值、提现、归集都围绕 `txid + vout` 管理，不能只按地址余额建模 |
| 共识 | PoW，最长有效链/累计工作量 | 存在概率性重组，充值确认数必须保守配置 |
| 原生币精度 | 8 位，1 BTC = 100,000,000 satoshi | 全部金额使用整数 satoshi，禁止 `float64` |
| Token/NFT | BTC 主协议无账户型 Token；存在 Ordinals/BRC-20、Runes 等链上铭文/协议 | 交易所普通 BTC 钱包不应默认支持这些资产；若支持需独立索引器和风控 |
| Memo/Tag | 不需要 Memo/Tag | 每个用户分配独立 BTC 地址，不用共享地址 + memo |
| 签名算法 | secp256k1 ECDSA；Taproot 支持 Schnorr | 初期建议支持 P2WPKH ECDSA，Taproot 可后续扩展 |
| 主流地址 | Legacy P2PKH、P2SH、Native SegWit P2WPKH、Taproot P2TR | 新地址建议使用 bech32 P2WPKH，兼顾手续费和兼容性 |
| 手续费 | 按交易虚拟字节 vB 计费，`fee = vsize * sat/vB` | 提现/归集必须估算输入数、输出数、找零和费率 |
| RPC 模型 | Bitcoin Core 原生 RPC 不提供任意地址交易历史 | 钱包必须扫块解析，或引入 Electrum/Esplora/自建索引器 |

## 2. 密码学算法与地址生成

### 2.1 私钥、公钥和签名

BTC 使用 secp256k1 曲线。传统 P2PKH/P2SH-P2WPKH/P2WPKH 交易使用 ECDSA 签名；Taproot P2TR 使用 Schnorr 签名。当前项目 `01-key-management` 已有 BIP32 + secp256k1 基础，BTC 可复用私钥派生、内存清除、远程签名审计等思路，但签名消息构造与 EVM 完全不同。

生产建议：

- 地址派生遵循 BIP32/BIP44/BIP84，避免继续使用 `m/fnv1a(chain)'/index'` 这种教学路径作为 BTC 生产路径。
- BTC P2WPKH 推荐路径：`m/84'/0'/account'/change/address_index`。
- 测试网 P2WPKH 路径：`m/84'/1'/account'/change/address_index`。
- `change = 0` 表示外部收款地址，`change = 1` 表示找零地址。
- 公钥建议存压缩格式 33 字节；P2WPKH 地址由 `HASH160(compressed_pubkey)` 编码得到。

### 2.2 地址类型

| 类型 | 示例前缀 | 脚本 | 建议 |
|------|----------|------|------|
| P2PKH Legacy | `1...` | `OP_DUP OP_HASH160 <pubKeyHash> OP_EQUALVERIFY OP_CHECKSIG` | 兼容性好，手续费高，不建议新发 |
| P2SH | `3...` | `OP_HASH160 <scriptHash> OP_EQUAL` | 常用于嵌套 SegWit 或多签，手续费中等 |
| P2WPKH Native SegWit | `bc1q...` | witness v0 + 20 字节 pubKeyHash | 推荐作为初期 BTC 托管地址 |
| P2TR Taproot | `bc1p...` | witness v1 + 32 字节 x-only pubkey | 可后续支持，需 Schnorr/Taproot sighash |

初期落地建议：只给用户派生 P2WPKH `bc1q` 地址，热钱包/冷钱包也优先 P2WPKH。先不混用 Legacy/Taproot，降低签名和手续费估算复杂度。

## 3. 公钥到地址编码

P2WPKH 地址生成流程：

1. BIP32 派生 secp256k1 子私钥。
2. 计算压缩公钥，33 字节。
3. `pubKeyHash = RIPEMD160(SHA256(compressed_pubkey))`。
4. 构造 witness program：version = 0，program = 20 字节 `pubKeyHash`。
5. 使用 bech32 编码，主网 HRP 为 `bc`，测试网为 `tb`。

示例：

```text
compressed_pubkey = 02...
pubKeyHash        = HASH160(compressed_pubkey)
scriptPubKey      = 0014{20-byte-pubKeyHash}
address           = bc1q...
```

注意事项：

- BTC 地址区分网络，主网 `bc1`，testnet/signet `tb1`，regtest `bcrt1`。
- 入库地址长度不能沿用 EVM 的 `VARCHAR(42)`，建议统一改成 `VARCHAR(120)`。
- 交易哈希在 RPC 中通常按人类可读小端字符串展示，原始交易内部字节序不同；入库统一使用 RPC 展示的 txid 字符串。

## 4. 交易构建与签名

BTC 交易由 inputs 和 outputs 组成：

- input 引用上一笔交易的 `txid + vout`，并提供 `scriptSig`/`witness` 解锁。
- output 包含 `value` 和 `scriptPubKey`。
- 没有 Nonce。防双花靠每个 UTXO 只能被花费一次。

### 4.1 P2WPKH 签名所需参数

离线签名不能只传 32 字节 `messageHash`。P2WPKH 的签名哈希构造需要知道被花费 UTXO 的金额和脚本信息。签名服务至少需要：

- unsigned transaction：版本、输入、输出、locktime。
- 每个 input 对应的 previous outpoint：`prev_txid`、`prev_vout`。
- 每个 input 的 `amount`，单位 satoshi。
- 每个 input 的 `scriptPubKey` 或可推导出的 `scriptCode`。
- sighash 类型，通常为 `SIGHASH_ALL`。
- 派生路径或签名地址，用于定位私钥。

钱包服务必须在签名前做策略校验：

- 输出地址必须等于提现目标地址、归集地址或本系统找零地址。
- 找零地址必须归属本系统，且与业务链/账户类型匹配。
- 交易总输入 = 外部输出 + 找零 + 手续费。
- 手续费不超过风控阈值，且 `sat/vB` 在当前网络合理范围。
- 签名后必须本地验签/反解析 rawtx，确认 txid、输入输出、手续费与预期一致，再广播。

### 4.2 原始交易构建流程

1. 根据提现/归集目标选择可用 UTXO。
2. 估算交易 vsize 和手续费。
3. 生成输出：目标输出、必要时找零输出。
4. 扣除手续费，处理 dust 阈值以下找零。
5. 构造 unsigned tx。
6. 对每个 input 计算 BIP143 sighash。
7. 远程签名服务返回 DER ECDSA 签名 + sighash byte。
8. 组装 witness：`<signature> <compressed_pubkey>`。
9. 序列化 rawtx，计算 txid/wtxid。
10. 广播并落库锁定输入 UTXO。

## 5. 链特性

### 5.1 链简单原理

BTC 区块包含区块头和交易列表。区块头中 `previousblockhash` 指向父块，钱包同步器可沿用本项目 `parentHash` 检测思路：

- 本地保存 `height -> hash,parent`。
- 扫新区块时校验 `new.parent == local.back.hash`。
- 不一致说明发生重组，向前查找共同祖先。
- 回滚被替换区块中的充值、提现、系统交易和 UTXO 状态。

### 5.2 共识和确认数

BTC 是 PoW，不支持 PoS/DPoS 质押。交易所一般至少使用 3 到 6 个确认，金额越大确认数越高。建议本项目初始配置：

- 小额充值：3 确认，仅教学/demo 使用。
- 普通生产充值：6 确认。
- 大额充值：12 确认或人工复核。
- 提现成功状态：交易进块后先标记 `PendingOnChain`，达到确认数后标记 `Success`。

### 5.3 账户模型和 UTXO

BTC 没有链上账户余额。地址余额是钱包索引器根据未花费输出聚合出来的结果。

对交易所而言：

- 用户充值地址收到的 output 是用户 UTXO。
- 提现/归集会消费一个或多个 UTXO。
- 交易可能产生找零 output，找零必须回到系统控制地址。
- 同一笔 BTC 交易可以同时包含多个输入和多个输出，甚至同时命中多个用户地址。

### 5.4 Token、NFT 和同源链

BTC 主链没有 EVM 式合约 Token。Ordinals、BRC-20、Runes 等协议不是 Bitcoin Core 原生资产余额模型，需要额外索引器，且对交易构建有更高风控要求，避免误花带铭文/稀有聪的 UTXO。

同源/类似 UTXO 链包括 LTC、BCH、DOGE、DASH、ZEC 等，但地址编码、签名哈希、费用市场和 RPC 细节各不相同，不能简单复用 BTC 参数。

## 6. RPC 调研

### 6.1 推荐节点接口

基础可用方案：Bitcoin Core 全节点 + JSON-RPC。

关键 RPC：

| 能力 | RPC | 说明 |
|------|-----|------|
| 获取同步状态 | `getblockchaininfo` | 获取 `blocks`、`headers`、`verificationprogress`、`initialblockdownload` |
| 获取网络状态 | `getnetworkinfo` | 节点版本、连接数等 |
| 获取最新区块高度 | `getblockcount` | 当前 best height |
| 高度查哈希 | `getblockhash` | `height -> block hash` |
| 获取区块 | `getblock` | verbosity=2 时返回区块及交易详情 |
| 获取交易详情 | `getrawtransaction` | 需 txindex 或交易在钱包/内存池/指定 block 内 |
| 查询 UTXO 是否未花费 | `gettxout` | 可用于二次校验某个 outpoint 是否仍未花费 |
| 广播交易 | `sendrawtransaction` | 广播 hex rawtx |
| 估算费率 | `estimatesmartfee` | 返回目标确认块数的推荐 fee rate |
| Mempool 条目 | `getmempoolentry` | 查询未确认交易费率、祖先/后代信息 |
| 扫描 UTXO 集 | `scantxoutset` | 可按 descriptor 扫描链上 UTXO，适合初始化/对账，不适合高频业务 |

### 6.2 余额和地址交易记录

Bitcoin Core 不支持类似 `eth_getBalance(address)` 的任意地址余额查询，也不提供普通地址维度交易列表。可选方案：

1. 自建扫块索引：本项目推荐。解析每个新区块交易，筛选命中托管地址的 vout/vin，维护本地 UTXO 表。
2. Bitcoin Core wallet：可导入 descriptor/watch-only 地址，但对交易所多用户地址、历史索引和业务状态机不够透明。
3. Electrum Server / Esplora / Blockbook：可作为数据平台或二次校验服务，但生产记账仍应以自建索引和自有节点为主。

### 6.3 区块解析策略

建议用 `getblock(hash, 2)` 拉完整交易数据：

- 遍历每笔交易的 `vout`，解析 `scriptPubKey.address` 或 `scriptPubKey.type`/`hex`，命中本系统地址则插入 UTXO。
- 遍历每笔交易的 `vin`，根据 `txid + vout` 查本地 UTXO，命中则标记为已花费，并记录 `spend_txid`、`spend_height`。
- coinbase 交易没有普通 `vin.txid/vout`，只解析其输出即可；交易所一般不会接收 coinbase 作为用户直接充值，若接收需考虑 coinbase maturity。

## 7. 节点部署调研

### 7.1 Bitcoin Core 配置建议

生产扫块节点建议：

```ini
server=1
txindex=1
prune=0
rpcbind=127.0.0.1
rpcallowip=127.0.0.1
rpcauth=...
zmqpubrawblock=tcp://127.0.0.1:28332
zmqpubrawtx=tcp://127.0.0.1:28333
dbcache=4096
```

说明：

- `txindex=1`：便于按 txid 查询历史交易；扫块本身不强依赖，但排障和二次校验强烈建议开启。
- `prune=0`：交易所钱包不建议使用裁剪节点，否则历史重扫、审计和深度修复受限。
- RPC 只监听内网或本机，禁止公网暴露。
- ZMQ 可用于新区块通知，但业务仍应以高度轮询和 parentHash 校验兜底。
- 至少部署两套独立节点，最好不同机房/不同磁盘，支持二次校验和故障切换。

### 7.2 初始化同步

初始化流程：

1. 节点完成 IBD，同步状态 `initialblockdownload=false`。
2. 导入/加载系统已派生地址集合到钱包服务本地索引。
3. 从业务起始高度扫描历史区块。
4. 生成本地 UTXO 集和充值记录。
5. 用第二节点或数据平台抽样核对余额与交易。

如果项目已有用户历史地址，不能只从当前高度开始扫；必须从最早地址启用高度或迁移快照开始。

## 8. 费用模型

BTC 手续费不是按金额比例收取，而是按交易虚拟大小：

```text
fee_sat = vsize_vB * fee_rate_sat_per_vB
```

P2WPKH 常用估算：

```text
vsize ≈ 10 + 68 * input_count + 31 * output_count
```

示例：1 输入 2 输出，费率 20 sat/vB：

```text
vsize ≈ 10 + 68*1 + 31*2 = 140 vB
fee   ≈ 140 * 20 = 2800 sat
```

工程要点：

- 输入越多手续费越高；归集小额碎片 UTXO 会消耗大量手续费。
- 需要 dust 阈值，找零低于 dust 时直接并入手续费。
- 高峰期提现可按 SLA 分档：慢速/普通/快速。
- 支持 RBF 时，输入 sequence 需低于 `0xfffffffe`；加速交易必须保持输出策略安全，避免改变用户目标输出。
- CPFP 可作为接收低费充值的加速手段，但交易所通常不为外部充值主动承担 CPFP，除非业务有明确 SLA。

## 9. 历史重组和风险

BTC 主网相对稳定，但不是不会重组。钱包必须假定：

- 1 到 2 块重组可能发生。
- 深度重组概率低但影响极高。
- 未确认交易可能被 RBF 替换或双花。
- mempool 交易不是入账依据，只能作为风控参考。

风控建议：

- 用户充值必须达到确认数才上账。
- 大额充值使用更高确认数和人工/风控审批。
- 本地同步器发现超过配置深度的重组时暂停入账和出账，触发告警。
- 回滚必须包含 UTXO 恢复，否则被回滚交易花费的 UTXO 会在本地永久丢失。

## 10. Memo/Tag

BTC 不需要 Memo/Tag。交易所应给每个用户派生独立充值地址。

原因：

- 派生 BTC 地址成本低。
- UTXO 钱包使用共享地址会让充值归属复杂化，不利于风控、隐私和对账。
- 如果为了节省地址而共享，会导致所有用户充值进入同一 UTXO 集，后续拆分、找零、归集都更难审计。

## 11. 浏览器和开发资料

官方/权威资料：

- Bitcoin Core RPC 文档：https://bitcoincore.org/en/doc/
- Bitcoin Developer Reference：https://developer.bitcoin.org/reference/
- BIP32 HD Wallets：https://bips.dev/32/
- BIP39 Mnemonic：https://bips.dev/39/
- BIP44 Multi-account hierarchy：https://bips.dev/44/
- BIP84 P2WPKH derivation：https://bips.dev/84/
- BIP141 SegWit：https://bips.dev/141/
- BIP143 SegWit v0 sighash：https://bips.dev/143/
- BIP173 Bech32 地址：https://bips.dev/173/
- BIP341 Taproot：https://bips.dev/341/
- BIP125 RBF：https://bips.dev/125/

常用浏览器/数据平台：

- mempool.space：https://mempool.space/
- Blockstream Explorer：https://blockstream.info/
- Blockchain.com Explorer：https://www.blockchain.com/explorer
- CoinMarketCap BTC：https://coinmarketcap.com/currencies/bitcoin/

## 12. Vin 和 Vout 管理

原提纲中的两张表：

```go
type Vins struct {
    GUID             uuid.UUID `gorm:"primaryKey" json:"guid"`
    Address          string    `json:"address"`
    TxId             string    `gorm:"column:tx_id" json:"tx_id"`
    Vout             uint8     `json:"vout"`
    Script           string    `json:"script"`
    Witness          string    `json:"witness"`
    Amount           *big.Int  `gorm:"serializer:u256" json:"amount"`
    SpendTxHash      string    `json:"spend_tx_hash"`
    SpendBlockHeight *big.Int  `gorm:"serializer:u256" json:"spend_block_height"`
    IsSpend          bool      `json:"is_spend"`
    Timestamp        uint64    `json:"timestamp"`
}

type Vouts struct {
    GUID      uuid.UUID `gorm:"primaryKey" json:"guid"`
    Address   string    `json:"address"`
    N         uint8     `json:"n"`
    Script    string    `json:"script"`
    Amount    *big.Int  `gorm:"serializer:u256" json:"amount"`
    Timestamp uint64    `json:"timestamp"`
}
```

不建议这样拆。问题：

- `vout`/`n` 使用 `uint8` 不够，BTC 一笔交易输出数量理论上可超过 255，建议 `uint32` 或数据库 `INT UNSIGNED`。
- `Vouts` 缺少 `txid`、`chain`、`block_height`、`block_hash`，无法唯一定位和回滚。
- `Vins` 把 input 和 UTXO 状态混在一起，`Address` 对外部输入经常无法可靠获得。
- `SpendBlockHeight *big.Int` 没必要，区块高度用 `BIGINT`/`int64`。
- `IsSpend bool` 不如状态枚举，无法表达 `locked`、`pending_spend`、`reverted`、`reserved`。
- `Script`/`Witness` 可能很长，建议 raw hex 用 `TEXT`，但业务表只保存必要字段。
- 缺少唯一键 `chain, txid, vout` 和 spend 索引。

### 12.1 推荐表结构

核心用一张 `wallet_utxos` 表表达“本系统可管理或曾管理的输出”，再用交易表记录完整交易关系。

```sql
CREATE TABLE `wallet_utxos` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY,
    `chain` VARCHAR(20) NOT NULL COMMENT 'BTC',
    `txid` VARCHAR(64) NOT NULL COMMENT '产生该 UTXO 的交易 hash',
    `vout` INT UNSIGNED NOT NULL COMMENT '输出序号',
    `address` VARCHAR(120) NOT NULL COMMENT '本系统地址',
    `uid` BIGINT NOT NULL DEFAULT 0 COMMENT '用户ID，系统地址为0',
    `address_kind` TINYINT NOT NULL COMMENT '0=User 1=Hot 2=Cold 3=Change',
    `symbol` VARCHAR(20) NOT NULL DEFAULT 'BTC',
    `amount` VARCHAR(40) NOT NULL COMMENT 'satoshi 整数字符串',
    `script_pubkey` TEXT NOT NULL COMMENT '锁定脚本 hex',
    `script_type` VARCHAR(32) NOT NULL COMMENT 'witness_v0_keyhash/pubkeyhash/scripthash/witness_v1_taproot',
    `block_height` BIGINT NOT NULL DEFAULT 0 COMMENT '产生高度，0=未确认',
    `block_hash` VARCHAR(64) NOT NULL DEFAULT '',
    `confirmations` INT UNSIGNED NOT NULL DEFAULT 0,
    `status` TINYINT NOT NULL DEFAULT 0 COMMENT '0=Available 1=Reserved 2=PendingSpend 3=Spent 4=Reverted 5=DustFrozen',
    `spend_txid` VARCHAR(64) NOT NULL DEFAULT '',
    `spend_height` BIGINT NOT NULL DEFAULT 0,
    `reserved_by` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '提现单/归集任务ID',
    `reserved_at` DATETIME NULL,
    `btime` DATETIME NOT NULL,
    `ctime` DATETIME NOT NULL,
    `mtime` DATETIME NOT NULL,
    UNIQUE KEY `uk_outpoint` (`chain`, `txid`, `vout`),
    KEY `idx_address_status` (`chain`, `address`, `status`),
    KEY `idx_status_amount` (`chain`, `status`, `amount`),
    KEY `idx_spend` (`chain`, `spend_txid`),
    KEY `idx_height` (`chain`, `block_height`)
) ENGINE=InnoDB COMMENT='BTC UTXO 状态表';
```

交易索引表：

```sql
CREATE TABLE `wallet_btc_txs` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY,
    `chain` VARCHAR(20) NOT NULL,
    `txid` VARCHAR(64) NOT NULL,
    `wtxid` VARCHAR(64) NOT NULL DEFAULT '',
    `block_height` BIGINT NOT NULL DEFAULT 0,
    `block_hash` VARCHAR(64) NOT NULL DEFAULT '',
    `tx_index` INT UNSIGNED NOT NULL DEFAULT 0,
    `version` INT NOT NULL,
    `lock_time` BIGINT NOT NULL DEFAULT 0,
    `size` INT UNSIGNED NOT NULL DEFAULT 0,
    `vsize` INT UNSIGNED NOT NULL DEFAULT 0,
    `weight` INT UNSIGNED NOT NULL DEFAULT 0,
    `fee` VARCHAR(40) NOT NULL DEFAULT '0',
    `direction` TINYINT NOT NULL DEFAULT 0 COMMENT '0=External 1=Inbound 2=Outbound 3=Sweep 4=InternalMixed',
    `status` TINYINT NOT NULL DEFAULT 0 COMMENT '0=Unconfirmed 1=Confirmed 2=Reverted',
    `btime` DATETIME NULL,
    `ctime` DATETIME NOT NULL,
    `mtime` DATETIME NOT NULL,
    UNIQUE KEY `uk_txid` (`chain`, `txid`),
    KEY `idx_height` (`chain`, `block_height`),
    KEY `idx_status` (`chain`, `status`)
) ENGINE=InnoDB COMMENT='BTC 交易索引表';
```

输入明细表用于审计和回滚：

```sql
CREATE TABLE `wallet_btc_tx_inputs` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY,
    `chain` VARCHAR(20) NOT NULL,
    `txid` VARCHAR(64) NOT NULL COMMENT '当前交易',
    `vin_index` INT UNSIGNED NOT NULL,
    `prev_txid` VARCHAR(64) NOT NULL,
    `prev_vout` INT UNSIGNED NOT NULL,
    `prev_address` VARCHAR(120) NOT NULL DEFAULT '',
    `prev_amount` VARCHAR(40) NOT NULL DEFAULT '0',
    `script_sig` TEXT,
    `sequence` BIGINT UNSIGNED NOT NULL,
    `witness` MEDIUMTEXT,
    `is_ours` TINYINT NOT NULL DEFAULT 0,
    `block_height` BIGINT NOT NULL DEFAULT 0,
    `ctime` DATETIME NOT NULL,
    UNIQUE KEY `uk_input` (`chain`, `txid`, `vin_index`),
    KEY `idx_prevout` (`chain`, `prev_txid`, `prev_vout`),
    KEY `idx_height` (`chain`, `block_height`)
) ENGINE=InnoDB COMMENT='BTC 交易输入明细';
```

输出明细表用于完整审计；如果只做教学 demo，可先只保留 `wallet_utxos`：

```sql
CREATE TABLE `wallet_btc_tx_outputs` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY,
    `chain` VARCHAR(20) NOT NULL,
    `txid` VARCHAR(64) NOT NULL,
    `vout` INT UNSIGNED NOT NULL,
    `address` VARCHAR(120) NOT NULL DEFAULT '',
    `amount` VARCHAR(40) NOT NULL,
    `script_pubkey` TEXT NOT NULL,
    `script_type` VARCHAR(32) NOT NULL DEFAULT '',
    `is_ours` TINYINT NOT NULL DEFAULT 0,
    `block_height` BIGINT NOT NULL DEFAULT 0,
    `ctime` DATETIME NOT NULL,
    UNIQUE KEY `uk_output` (`chain`, `txid`, `vout`),
    KEY `idx_address` (`chain`, `address`),
    KEY `idx_height` (`chain`, `block_height`)
) ENGINE=InnoDB COMMENT='BTC 交易输出明细';
```

### 12.2 UTXO 状态机

```text
Available -> Reserved       选币时加业务锁，防并发双花
Reserved  -> PendingSpend   rawtx 已签名/待广播或已广播未确认
PendingSpend -> Spent       花费交易确认
Reserved/PendingSpend -> Available  广播失败、任务取消、RBF 替换失败或超时释放
Available/PendingSpend/Spent -> Reverted  产生该 UTXO 的区块被回滚
Spent -> Available          花费该 UTXO 的交易所在区块被回滚
```

关键约束：

- 选币必须在 DB 事务中 `SELECT ... FOR UPDATE` 锁住候选 UTXO。
- 广播前就应把输入从 `Available` 变成 `PendingSpend`，避免并发提现复用同一个 UTXO。
- 如果广播失败且交易未进入 mempool，可释放为 `Available`。
- 如果交易进入 mempool 但未确认，不能随便释放；需要 RBF/双花检测后处理。

## 13. 交易示例：如何转成用户 UTXO 记录

假设系统托管地址：

```text
用户1001充值地址 A = bc1q_user1001...
用户1002充值地址 B = bc1q_user1002...
热钱包地址       H = bc1q_hot...
找零地址         C = bc1q_change...
```

### 13.1 外部用户充值

链上交易 `txid = tx_deposit_1`：

```json
{
  "txid": "tx_deposit_1",
  "vin": [
    {"txid": "external_prev", "vout": 0}
  ],
  "vout": [
    {
      "n": 0,
      "value_sat": 5000000,
      "scriptPubKey": {
        "type": "witness_v0_keyhash",
        "address": "bc1q_user1001..."
      }
    },
    {
      "n": 1,
      "value_sat": 1200000,
      "scriptPubKey": {
        "type": "witness_v0_keyhash",
        "address": "bc1q_external_change..."
      }
    }
  ],
  "block_height": 800000,
  "block_hash": "block_hash_800000"
}
```

解析步骤：

1. 遍历 vout[0]，地址命中 `keyman_derived_addresses` 或 `wallet_accounts`。
2. 插入 `wallet_utxos`：

```text
chain=BTC
txid=tx_deposit_1
vout=0
address=bc1q_user1001...
uid=1001
address_kind=User
amount=5000000
script_type=witness_v0_keyhash
block_height=800000
status=Available
```

3. 插入 `wallet_inbound`：

```text
chain=BTC
symbol=BTC
height=800000
hash=tx_deposit_1
from='' 或 external_prev 推导出的地址
to=bc1q_user1001...
value=5000000
uid=1001
status=Pending/Success 取决于确认数
```

4. 当安全高度达到 `800000 + confirms`，充值上账，写入 `wallet_balance_log`。

### 13.2 归集交易

系统将用户 UTXO 归集到热钱包，交易 `tx_sweep_1`：

```json
{
  "txid": "tx_sweep_1",
  "vin": [
    {"txid": "tx_deposit_1", "vout": 0}
  ],
  "vout": [
    {
      "n": 0,
      "value_sat": 4997000,
      "scriptPubKey": {
        "type": "witness_v0_keyhash",
        "address": "bc1q_hot..."
      }
    }
  ],
  "fee_sat": 3000,
  "block_height": 800010
}
```

解析步骤：

1. 遍历 vin，发现 `tx_deposit_1:0` 是本系统 UTXO。
2. 更新原 UTXO：

```text
status=Spent
spend_txid=tx_sweep_1
spend_height=800010
```

3. 遍历 vout[0]，地址 `H` 是系统热钱包地址，插入新 UTXO：

```text
txid=tx_sweep_1
vout=0
address=bc1q_hot...
uid=0
address_kind=Hot
amount=4997000
status=Available
```

4. 插入 `wallet_system_txs`，`kind=sweep`，`fee=3000` 可记录在 BTC 交易表或扩展系统交易表。

### 13.3 提现交易

用户提现 1,000,000 sat 到外部地址 `X`，系统使用热钱包 UTXO `tx_sweep_1:0`：

```json
{
  "txid": "tx_withdraw_1",
  "vin": [
    {"txid": "tx_sweep_1", "vout": 0}
  ],
  "vout": [
    {
      "n": 0,
      "value_sat": 1000000,
      "scriptPubKey": {"address": "bc1q_external_user..."}
    },
    {
      "n": 1,
      "value_sat": 3994000,
      "scriptPubKey": {"address": "bc1q_change..."}
    }
  ],
  "fee_sat": 3000,
  "block_height": 800020
}
```

解析步骤：

1. input 命中热钱包 UTXO `tx_sweep_1:0`，标记为 `Spent`。
2. vout[0] 是外部地址，不插入 `wallet_utxos`，但插入输出明细用于审计。
3. vout[1] 是系统找零地址，插入新 UTXO：

```text
txid=tx_withdraw_1
vout=1
address=bc1q_change...
uid=0
address_kind=Change
amount=3994000
status=Available
```

4. 更新 `wallet_outbound`：

```text
hash=tx_withdraw_1
height=800020
fee=3000
status=Success 或确认中状态
rawtx=<hex>
```

## 14. 交易记录管理

现有 `wallet_inbound` / `wallet_outbound` / `wallet_system_txs` 可以复用，但字段需要适配 BTC：

- `hash VARCHAR(66)` 应调整为兼容无 `0x` 的 64 字符 txid，建议统一 `VARCHAR(80)`。
- `from`/`to VARCHAR(42)` 不适合 BTC，建议 `VARCHAR(120)`。
- `nonce` 对 BTC 无意义，可保留为 0，但更好的方式是提现明细表拆出链特定字段。
- BTC 提现需要记录 input set、change output、fee_rate、vsize、RBF sequence。
- `fee` 单位为 satoshi，不是 gas。

建议新增 BTC 提现输入绑定表：

```sql
CREATE TABLE `wallet_btc_spend_inputs` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY,
    `chain` VARCHAR(20) NOT NULL,
    `business_type` VARCHAR(20) NOT NULL COMMENT 'withdraw/sweep',
    `business_id` BIGINT NOT NULL,
    `txid` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '花费交易 txid，签名前可为空',
    `prev_txid` VARCHAR(64) NOT NULL,
    `prev_vout` INT UNSIGNED NOT NULL,
    `amount` VARCHAR(40) NOT NULL,
    `address` VARCHAR(120) NOT NULL,
    `status` TINYINT NOT NULL DEFAULT 0 COMMENT '0=Reserved 1=Signed 2=Broadcast 3=Confirmed 4=Released',
    `ctime` DATETIME NOT NULL,
    `mtime` DATETIME NOT NULL,
    UNIQUE KEY `uk_business_input` (`chain`, `business_type`, `business_id`, `prev_txid`, `prev_vout`),
    KEY `idx_prevout` (`chain`, `prev_txid`, `prev_vout`)
) ENGINE=InnoDB COMMENT='BTC 提现/归集选币输入绑定';
```

建议新增 BTC rawtx 表：

```sql
CREATE TABLE `wallet_btc_raw_txs` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY,
    `chain` VARCHAR(20) NOT NULL,
    `business_type` VARCHAR(20) NOT NULL COMMENT 'withdraw/sweep/rbf',
    `business_id` BIGINT NOT NULL,
    `txid` VARCHAR(64) NOT NULL DEFAULT '',
    `rawtx` MEDIUMBLOB NOT NULL,
    `fee` VARCHAR(40) NOT NULL,
    `fee_rate` VARCHAR(40) NOT NULL COMMENT 'sat/vB，可用定点字符串',
    `vsize` INT UNSIGNED NOT NULL,
    `rbf` TINYINT NOT NULL DEFAULT 1,
    `status` TINYINT NOT NULL DEFAULT 0 COMMENT '0=Built 1=Signed 2=Broadcast 3=Confirmed 4=Failed 5=Replaced',
    `ctime` DATETIME NOT NULL,
    `mtime` DATETIME NOT NULL,
    UNIQUE KEY `uk_txid` (`chain`, `txid`),
    KEY `idx_business` (`chain`, `business_type`, `business_id`)
) ENGINE=InnoDB COMMENT='BTC 原始交易和费率记录';
```

## 15. 对本项目的落地建议

### 15.1 coinset 配置

在 `internal/coinset/chain.go` 增加：

```go
BTC = Chain{
    Name:         "BTC",
    ChainID:      0,
    Type:         UTXO,
    Confirms:     6,
    Features:     FeatureSegWit,
    NativeSymbol: "BTC",
    Decimals:     8,
}
```

同时在 `FeatureGate` 增加 UTXO/BTC 相关特性：

```go
FeatureSegWit
FeatureTaproot
FeatureRBF
```

### 15.2 模块拆分

建议新增 `08-btc-manager/demo/`，分阶段实现：

1. 地址生成：BIP84 P2WPKH 地址派生 demo。
2. 区块解析：解析 mock BTC block，生成 inbound 和 UTXO。
3. UTXO 回滚：模拟 2 块重组，验证 `Spent -> Available` 和新 UTXO 删除。
4. 选币和手续费：给定提现金额和 fee rate，选择 UTXO、生成找零。
5. 离线签名：P2WPKH rawtx 构建、签名、验签。
6. 对账：本地 UTXO sum 与节点 `scantxoutset`/第二索引器结果比对。

### 15.3 最小生产闭环

最小可上线 BTC 主币能力应包含：

- P2WPKH 地址派生和地址归属表。
- Bitcoin Core 双节点。
- 扫块同步 + parentHash 重组检测。
- UTXO 表和输入/输出明细表。
- 充值确认上账。
- 选币、找零、手续费估算。
- 远程签名和签后反解析校验。
- 广播、RBF 加速或人工重发策略。
- UTXO 级别对账。
- 深度重组暂停和告警。

不建议初期包含：

- Ordinals/BRC-20/Runes。
- Taproot 找零混用。
- 外部 API 作为唯一记账来源。
- 未确认充值上账。
- 自动花费未知来源或未完成归属校验的 UTXO。


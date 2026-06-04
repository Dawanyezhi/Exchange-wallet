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

### 4.3 生产示例：P2WPKH 提现构建与离线签名

下面示例以“热钱包 P2WPKH UTXO 提现到外部 P2WPKH 地址，并把找零打回系统找零地址”为准。生产系统中，在线钱包服务只负责选币、构建 unsigned tx、策略校验、签后反解析和广播；签名机/HSM 只接收最小必要交易上下文，独立重算 sighash 并做二次策略校验，不能信任在线服务传来的 32 字节摘要。

示例输入：

```json
{
  "chain": "BTC",
  "network": "mainnet",
  "business_type": "withdraw",
  "business_id": 900001,
  "withdraw_to": "bc1q_external_user_address...",
  "withdraw_amount_sat": "1000000",
  "fee_rate_sat_vb": "20",
  "change_address": "bc1q_system_change_address...",
  "inputs": [
    {
      "prev_txid": "7b1eabe0209b1fe794124575ef807057c77ada2138ae4fa8d6c4de0398a14f3f",
      "prev_vout": 0,
      "amount_sat": "1500000",
      "address": "bc1q_hot_wallet_address...",
      "script_pubkey": "0014{20-byte-hot-pubkey-hash-hex}",
      "derivation_path": "m/84'/0'/0'/1/25",
      "compressed_pubkey": "02..."
    }
  ]
}
```

费用和输出计算：

```text
input_count      = 1
output_count     = 2
estimated_vsize  = 10 + 68*1 + 31*2 = 140 vB
fee_sat          = 140 * 20 = 2800
change_sat       = 1500000 - 1000000 - 2800 = 497200
```

找零 `497200 sat` 高于 dust 阈值，因此生成两个输出：

```json
[
  {
    "address": "bc1q_external_user_address...",
    "amount_sat": "1000000",
    "purpose": "withdraw_target"
  },
  {
    "address": "bc1q_system_change_address...",
    "amount_sat": "497200",
    "purpose": "system_change"
  }
]
```

在线钱包服务构建 unsigned tx 时的关键约束：

- 所有金额使用 `int64` satoshi 或十进制整数字符串入库，接口层禁止使用 BTC 小数参与计算。
- input 必须来自 `wallet_utxos.status = Available`，选币事务内 `SELECT ... FOR UPDATE` 后改为 `Reserved`。
- `script_pubkey` 必须来自本地 UTXO 表或可信扫块索引，不能由提现请求方传入。
- 找零地址必须来自本系统已登记的 change 派生路径，不能由业务请求直接指定。
- 输出顺序可以固定，也可以随机化；但签名前、签名后都必须按“目标输出、找零输出、手续费”重新校验。
- 若启用 RBF，input `sequence` 使用低于 `0xfffffffe` 的值。`0xfffffffe` 是 32 位无符号整数 `4294967294`，常用可替换值为 `0xfffffffd`，即 `4294967293`；否则使用 `0xffffffff`，即 `4294967295`，表示 final sequence。

在线钱包服务生成的签名请求不要只包含 sighash，建议使用如下结构：

```json
{
  "request_id": "sign-900001-1",
  "chain": "BTC",
  "network": "mainnet",
  "tx_version": 2,
  "lock_time": 0,
  "sighash_type": "ALL",
  "inputs": [
    {
      "index": 0,
      "prev_txid": "7b1eabe0209b1fe794124575ef807057c77ada2138ae4fa8d6c4de0398a14f3f",
      "prev_vout": 0,
      "amount_sat": "1500000",
      "script_pubkey": "0014{20-byte-hot-pubkey-hash-hex}",
      "script_code": "1976a914{20-byte-hot-pubkey-hash-hex}88ac",
      "derivation_path": "m/84'/0'/0'/1/25",
      "expected_pubkey": "02..."
    }
  ],
  "outputs": [
    {
      "index": 0,
      "address": "bc1q_external_user_address...",
      "amount_sat": "1000000",
      "purpose": "withdraw_target"
    },
    {
      "index": 1,
      "address": "bc1q_system_change_address...",
      "amount_sat": "497200",
      "purpose": "system_change",
      "derivation_path": "m/84'/0'/0'/1/26"
    }
  ],
  "policy": {
    "max_fee_sat": "20000",
    "max_fee_rate_sat_vb": "100",
    "allowed_output_purposes": ["withdraw_target", "system_change"],
    "require_change_owned_by_system": true
  }
}
```

签名机/HSM 处理逻辑：

1. 校验 `chain/network/business_type` 是否在签名策略允许范围内。
2. 根据 `derivation_path` 派生私钥，计算压缩公钥，必须等于 `expected_pubkey`。
3. 从 `script_pubkey = 0014{pubKeyHash}` 推导 P2WPKH `script_code = 1976a914{pubKeyHash}88ac`，并与请求中的 `script_code` 比对。
4. 用完整 unsigned tx、所有 input 的 `amount_sat`、`script_code` 按 BIP143 计算每个 input 的 sighash。
5. 对 sighash 做 secp256k1 ECDSA 签名，编码为 DER，并追加 sighash byte `01`。
6. 返回每个 input 的 `signature` 和 `compressed_pubkey`，不返回私钥，不接受在线服务传来的预计算摘要作为唯一签名对象。

签名响应：

```json
{
  "request_id": "sign-900001-1",
  "approved": true,
  "signatures": [
    {
      "input_index": 0,
      "signature": "3044...01",
      "compressed_pubkey": "02..."
    }
  ],
  "signer_audit_id": "btc-hot-hsm-20260604-000001"
}
```

在线钱包服务收到签名后组装 witness：

```text
vin[0].scriptSig = empty
vin[0].witness   = [
  "3044...01",
  "02..."
]
```

组装 rawtx 后必须做广播前校验：

- 反序列化 rawtx，确认版本、locktime、input outpoint、sequence 没有被篡改。
- 重新解析每个 output 的 `scriptPubKey -> address`，确认提现目标金额等于业务订单，找零地址属于系统。
- 用本地 UTXO 金额重新计算 `fee = sum(inputs) - sum(outputs)`，确认不超过 `max_fee_sat` 和 `max_fee_rate_sat_vb`。
- 对每个 input 执行脚本验证，P2WPKH 至少启用 `SCRIPT_VERIFY_WITNESS`、`SCRIPT_VERIFY_P2SH`、`SCRIPT_VERIFY_DERSIG`、`SCRIPT_VERIFY_NULLDUMMY`、`SCRIPT_VERIFY_CHECKLOCKTIMEVERIFY`、`SCRIPT_VERIFY_CHECKSEQUENCEVERIFY` 等标准校验标志。
- 调用节点 `testmempoolaccept` 预检查；通过后再 `sendrawtransaction` 广播。
- 广播成功后写入 `wallet_btc_raw_txs`，并把输入 UTXO 从 `Reserved` 更新为 `PendingSpend`，记录 `spend_txid`。

本目录已按生产职责拆成两个可运行 demo：

#### 4.3.1 交易签名发送：`08-btc-manager/tx-sign-send/`

- `hd.go`：BIP32 secp256k1 私钥派生，用于演示 `m/84'/0'/account'/change/index`。
- `bech32.go`：P2WPKH `bc1q...` 地址编码/解码和 `scriptPubKey/scriptCode` 构造。
- `tx.go`：UTXO 输入上下文、找零计算、签名前策略校验、BIP143 sighash、DER ECDSA 签名、witness 组装、签后反解析验签、txid/wtxid/vsize/fee 计算。
- `main.go`：跑通“构建 unsigned tx -> 离线签名 -> 签后校验 -> 输出 rawtx”的提现流程。
- `btc_demo_test.go`：覆盖地址编码、P2WPKH 签名交易、签后输出解析和找零归属拒绝。

运行签名流程：

```bash
go run ./08-btc-manager/tx-sign-send
```

该 demo 会输出：

- 热钱包收款地址：`m/84'/0'/0'/0/0`。
- 热钱包找零地址：`m/84'/0'/0'/1/0`。
- 外部用户提现目标地址。
- unsigned tx 输入/输出/手续费。
- 签名机重算的 BIP143 sighash 和 DER 签名。
- 签后反解析得到的 `txid`、`wtxid`、`vsize`、`fee` 和 rawtx。

注意：demo 中 `prev_txid` 是演示用假 outpoint，不应广播到主网；生产真实交易必须从扫块索引里的 `Available UTXO` 选币，并在 DB 事务内锁定为 `Reserved/PendingSpend`。

#### 4.3.2 区块交易解析：`08-btc-manager/block-tx-parser/`

- `parser.go`：raw transaction/raw block 二进制解析，支持识别 coinbase、witness、P2WPKH/P2WSH/P2SH/P2PKH/P2TR/OP_RETURN 输出。
- `fetch.go`：Bitcoin Core `getblock(hash, 0)` RPC 和 Blockstream raw block 拉取示例。
- `bech32.go`、`util.go`：解析输出地址、txid/wtxid、vsize 所需的最小工具函数。
- `main.go`：提供 `parse-block` 和 `parse-tx` 两种入口。

解析用户给出的主网区块：

```bash
CGO_ENABLED=0 go run ./08-btc-manager/block-tx-parser \
  -mode parse-block \
  -block-hash 0000000000000000000162f179adec6f69571824971aa1fa5e78a6074be22864 \
  -source blockstream
```

生产建议使用自建 Bitcoin Core：

```bash
CGO_ENABLED=0 go run ./08-btc-manager/block-tx-parser \
  -mode parse-block \
  -block-hash 0000000000000000000162f179adec6f69571824971aa1fa5e78a6074be22864 \
  -source rpc \
  -rpc-url http://127.0.0.1:8332 \
  -rpc-user "$BTC_RPC_USER" \
  -rpc-pass "$BTC_RPC_PASS"
```

也可以解析本地 raw block 文件：

```bash
bitcoin-cli getblock 0000000000000000000162f179adec6f69571824971aa1fa5e78a6074be22864 0 > /tmp/block.hex
CGO_ENABLED=0 go run ./08-btc-manager/block-tx-parser -mode parse-block -raw-block-file /tmp/block.hex
```

当前本机 GVM/macOS 环境直接 `go run` 解析目录时可能遇到 `dyld: missing LC_UUID load command`，使用 `CGO_ENABLED=0` 走纯 Go 构建路径可稳定运行。生产部署建议固定 Go 工具链和构建参数，避免本机动态链接器差异影响运维脚本。

本次用公共 API 实测该区块摘要：

```text
hash       = 0000000000000000000162f179adec6f69571824971aa1fa5e78a6074be22864
height     = 952279
time       = 2026-06-04T02:20:09Z
tx_count   = 3574
size       = 1616968 bytes
weight     = 3995098
merkle     = 4c28d883363f25719167c880133829890b71cff9e72933f35ab1cbd995d720cb
prev_block = 00000000000000000001feb83a3f529b7be19db66ffd592abf69ddee2e0f7f8e
```

用本地下载的 raw block 解析得到的前几笔交易包括：

```text
coinbase txid = 7a71a2a1b42b8b587d45abfdd3059ff4a00562a851acb24088fe5fa9c1077e31
tx[1] txid    = 8b0794b7ec0c7751978b94eda84908f44871667b2a51537c768c3dba883b1d46
tx[2] txid    = e4ea5cb8d836ee5cad27f4a82f8bf069a1efbad1d76b6f93f0c4bbd1d801b056
```

交易所生产扫块不能只打印交易摘要，而要把解析结果和本地地址/UTXO 表匹配：

- 命中系统 `DEPOSIT` 地址的 `vout`：插入充值 UTXO，进入确认数状态机。
- 命中系统 `CHANGE` 地址的 `vout`：记为内部找零回流，不触发用户入账。
- `vin` 花费了本地 UTXO：更新为 `Spent/PendingSpend`，记录 `spend_txid/spend_height`。
- 遇到重组：按区块高度和 hash 回滚上述 UTXO/充值/提现状态。

如果使用 Bitcoin Core 做联调或验收，可以用 PSBT/RPC 路径校验构建结果，但生产不建议把交易所热私钥长期导入在线 Bitcoin Core 钱包：

```bash
# 1. 创建 PSBT，inputs/outputs 必须由钱包服务已校验数据生成。
bitcoin-cli walletcreatefundedpsbt \
  '[{"txid":"7b1eabe0209b1fe794124575ef807057c77ada2138ae4fa8d6c4de0398a14f3f","vout":0}]' \
  '[{"bc1q_external_user_address...":0.01000000},{"bc1q_system_change_address...":0.00497200}]' \
  0 \
  '{"replaceable":true,"fee_rate":20,"subtractFeeFromOutputs":[]}' \
  true

# 2. 分析 PSBT，确认缺少哪些签名和最终 fee/vsize。
bitcoin-cli analyzepsbt '<psbt>'

# 3. 离线签名完成后 finalize，得到 hex rawtx。
bitcoin-cli finalizepsbt '<signed_psbt>'

# 4. 广播前先进入 mempool 预检查。
bitcoin-cli testmempoolaccept '["<rawtx_hex>"]'

# 5. 预检查通过后广播。
bitcoin-cli sendrawtransaction '<rawtx_hex>'
```

RPC 金额字段使用 BTC 小数是 Bitcoin Core 接口格式限制；项目业务库和风控计算仍必须以 satoshi 整数为准，只在 RPC 边界做格式转换。

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

示例：1 输入 2 输出，费率 20 sat/vB。这里的 `20 sat/vB` 只用于演示手续费计算，不代表任何时间点一定能快速确认；BTC 确认速度取决于实时 mempool 拥堵程度、矿工打包策略和全网费率市场。

```text
vsize ≈ 10 + 68*1 + 31*2 = 140 vB
fee   ≈ 140 * 20 = 2800 sat
```

工程要点：

- 输入越多手续费越高；归集小额碎片 UTXO 会消耗大量手续费。
- 需要 dust 阈值，找零低于 dust 时直接并入手续费。
- 高峰期提现可按 SLA 分档：慢速/普通/快速。
- 生产不能固定写死 `20 sat/vB`。应结合 Bitcoin Core `estimatesmartfee`、自建 mempool 统计或可靠费率源，按目标确认块数动态给出慢速/普通/快速费率，并设置最低费率、最高费率和人工兜底策略。

### 8.1 生产手续费计算方案

生产中的 BTC 手续费计算建议拆成两层：

```text
fee_sat = estimated_vsize_vB * selected_fee_rate_sat_vB
```

- `estimated_vsize_vB` 由交易结构决定，包括 input 数、output 数、地址类型、是否 Taproot、多签脚本等。
- `selected_fee_rate_sat_vB` 由实时费率模型决定，不能使用固定常量。

费率来源建议按优先级组合，而不是单点依赖：

| 来源 | 作用 | 注意事项 |
|------|------|----------|
| 自建 mempool 统计 | 主费率来源，按目标确认块数估算慢速/普通/快速费率 | 需要节点稳定同步 mempool，并监控样本异常 |
| Bitcoin Core `estimatesmartfee` | 本地节点参考值 | 返回值可能偏保守，也可能因样本不足不可用 |
| 第三方费率 API | 兜底和交叉校验 | 不能作为唯一来源，避免 API 异常导致高费或低费 |
| 人工配置上下限 | 风控保护 | 避免极端行情或数据源异常时自动烧手续费 |

推荐 SLA 分档：

```text
slow     目标 6-12 块确认
normal   目标 2-6 块确认
fast     目标 1-2 块确认
urgent   目标下一块，需风控审批、VIP 策略或人工授权
```

费率选择可以使用如下策略：

```text
selected_fee_rate =
  clamp(
    max(core_estimate, mempool_estimate, external_estimate_adjusted),
    min_fee_rate,
    max_fee_rate
  )
```

其中 `clamp` 用于限制上下限，避免节点异常或第三方数据异常时报出离谱费率。`external_estimate_adjusted` 可按本地策略打折或加权，不能直接照搬外部 API。

生产提现计算流程：

1. 根据提现金额、目标地址类型、系统 UTXO 状态选择候选 UTXO。
2. 按地址类型估算初始 vsize。
3. 从费率服务获取 `slow/normal/fast/urgent` 对应的 `fee_rate_sat_vB`。
4. 计算 `fee_sat = vsize * fee_rate`。
5. 生成用户目标输出和系统找零输出。
6. 如果找零小于 dust 阈值，移除找零并并入手续费。
7. 找零变化可能改变 output 数量，需要重新估算 vsize 和 fee。
8. 校验手续费总额、费率、用户输出、找零归属和 input 归属。
9. 签名后反解析 rawtx，按真实 vsize 复算 `actual_fee_rate = fee / vsize`。
10. 调用 `testmempoolaccept` 预检查，通过后广播。

不同脚本类型 vsize 差异明显，生产不能只按 P2WPKH 公式估所有交易：

| 类型 | 常见估算 | 说明 |
|------|----------|------|
| P2PKH input | 约 148 vB | Legacy 输入，手续费高 |
| P2WPKH input | 约 68 vB | 初期推荐托管地址类型 |
| P2TR key path input | 约 58 vB | Taproot 单签路径，需 Schnorr/Taproot 支持 |
| P2WPKH output | 约 31 vB | Native SegWit 输出 |
| P2TR output | 约 43 vB | Taproot 输出 |

### 8.2 签名前估算与签后真实 vsize 校验

生产中建议明确区分两个字段：

```text
estimated_vsize_vb  签名前模板估算的虚拟字节数
actual_vsize_vb     签名后从 rawtx 反解析得到的真实虚拟字节数
```

#### 方案 1：签名前模板估算 `estimated_vsize_vb`

在构建交易前或选币完成后，根据输入/输出类型估算虚拟字节数。常见模板：

```text
tx overhead        ≈ 10 vB
P2WPKH input       ≈ 68 vB
P2TR input         ≈ 58 vB
P2SH-P2WPKH input  ≈ 91 vB
P2PKH input        ≈ 148 vB

P2WPKH output      ≈ 31 vB
P2TR output        ≈ 43 vB
P2SH output        ≈ 32 vB
P2PKH output       ≈ 34 vB
```

示例：

```text
1 个 P2WPKH input + 2 个 P2WPKH output

estimated_vsize_vb = 10 + 68*1 + 31*2
                   = 140 vB

estimated_fee_sat  = estimated_vsize_vb * fee_rate_sat_vb
change_sat         = input_sum_sat - withdraw_sat - estimated_fee_sat
```

用途：

- 选币。
- 手续费预估。
- 判断是否生成找零。
- 签名前风控检查。

注意：`estimated_vsize_vb` 是估算值，不是最终真实值。找零是否生成、输入数量变化、脚本类型变化都会导致它变化。

#### 方案 4：签名后反解析计算 `actual_vsize_vb`

交易签名完成后，已经能拿到完整 rawtx。广播前必须反序列化 rawtx，计算真实虚拟大小：

```text
stripped_size = 不包含 witness 的交易字节数
total_size    = 包含 witness 的完整 rawtx 字节数
witness_size  = total_size - stripped_size

weight        = stripped_size * 4 + witness_size
actual_vsize  = ceil(weight / 4)
```

然后复算真实手续费：

```text
actual_fee_sat      = input_sum_sat - output_sum_sat
actual_fee_rate_vb  = actual_fee_sat / actual_vsize_vb
```

广播前必须校验：

```text
actual_fee_sat <= max_fee_sat
actual_fee_rate_vb <= max_fee_rate_sat_vb
actual_fee_rate_vb >= min_fee_rate_sat_vb
abs(actual_vsize_vb - estimated_vsize_vb) 在可接受范围内
```

如果不满足上述条件，应拒绝广播，重新构建交易或进入人工处理。生产流程可以概括为：

```text
签名前:
  用模板估算 estimated_vsize_vb
  计算 estimated_fee_sat 和 change_sat

签名后:
  从 rawtx 反解析 actual_vsize_vb
  复算 actual_fee_sat 和 actual_fee_rate_sat_vb

广播前:
  只有 actual_* 校验通过，才允许 sendrawtransaction
```

风控参数建议至少包含：

```text
min_fee_rate_sat_vB
max_fee_rate_sat_vB
max_fee_sat_per_tx
max_fee_ratio = fee / withdraw_amount
dust_threshold_sat
max_input_count
max_change_outputs
sla_fee_policy
```

示例策略：

```text
普通提现：
  min_fee_rate = 2 sat/vB
  max_fee_rate = 200 sat/vB
  max_fee_sat = 100000 sat
  max_fee_ratio = 1%

大额或 VIP 提现：
  可使用更高 max_fee_rate，但必须经过策略授权、风控审批或人工确认。
```

最终落库字段建议记录：`fee_rate_source`、`fee_rate_level`、`selected_fee_rate_sat_vB`、`estimated_vsize_vb`、`actual_vsize_vb`、`estimated_fee_sat`、`actual_fee_sat`、`actual_fee_rate_sat_vb`、`max_fee_sat`、`fee_policy_snapshot`。这样后续 RBF、手续费复盘、用户争议和财务对账都有依据。

### 8.3 RBF 加速

RBF 是 Replace-By-Fee，即用“花费同一批 input、支付更高手续费”的新交易替换未确认旧交易。BIP125 规则中，只要交易任意 input 的 `sequence` 小于 `0xfffffffe`，就表示该交易显式允许被替换。`0xfffffffe` 是十六进制，等于十进制 `4294967294`；因此常见 RBF sequence 可使用 `0xfffffffd`，即 `4294967293`。如果不希望交易被替换，通常使用 `0xffffffff`，即 `4294967295`。

交易所提现支持 RBF 时，必须把“加速”限定为手续费调整，不允许借 RBF 改变业务输出：

- 用户目标输出地址必须与原提现单一致。
- 用户目标输出金额必须与原提现单一致。
- 新增手续费优先从系统找零输出扣减。
- 如果找零不足以覆盖加速费，不能直接减少用户输出，应进入人工处理、重新选币或按业务规则补充输入。
- 替换交易必须复用原交易 input，或者在明确策略下增加系统自有 input；新增 input 也必须来自已锁定的系统 UTXO。
- 签名前和签后反解析都要核对原提现单、旧 rawtx、新 rawtx 的目标输出、找零归属、input 归属和 fee delta。

示例：

```text
原提现：
input  = 1.00000000 BTC
用户输出 = 0.90000000 BTC
系统找零 = 0.09990000 BTC
fee      = 0.00010000 BTC

RBF 加速后：
input  = 1.00000000 BTC
用户输出 = 0.90000000 BTC  不变
系统找零 = 0.09980000 BTC  减少
fee      = 0.00020000 BTC  增加
```

### 8.4 CPFP 加速

CPFP 是 Child-Pays-For-Parent，即构造一笔高费率子交易花费未确认父交易的输出。矿工为了收取子交易的高手续费，需要同时打包父交易和子交易，因此可以间接加速低费父交易。

对交易所来说，CPFP 主要出现在“用户低费充值迟迟不确认”的场景：父交易是外部用户发给交易所充值地址的低费交易，交易所可以花费这个未确认充值输出，构造一笔高费率子交易，把币转到系统归集地址，从而提高父子交易整体费率。

工程边界：

- mempool 中的低费充值不能直接入账，CPFP 只解决确认速度，不改变确认数和假充值风控要求。
- 交易所通常不主动为外部充值承担 CPFP 手续费，因为低费父交易由用户发起，加速成本应有明确归属。
- 只有在 VIP、大额充值、明确 SLA、应急处置或向用户计费的产品规则下，才建议开放 CPFP。
- CPFP 子交易必须只花费已命中系统地址的未确认输出，目标必须是系统归集/热钱包地址，不能混入用户提现输出。
- 如果父交易后续被 RBF 双花替换，CPFP 子交易会失效；因此 CPFP 前仍要做双花、RBF 标记和多节点 mempool 检查。

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
- 缺少唯一键 `chain, txid, tx_out_index` 和 spend 索引。

### 12.1 推荐表结构

核心用一张 `wallet_utxos` 表表达“本系统可管理或曾管理的输出”，再用交易表记录完整交易关系。

命名建议：Bitcoin Core RPC 和区块原始结构中通常使用 `vout` 表示交易输出数组，文档解析链上 JSON 时可以保留 `vout`；但内部数据库字段建议使用 `tx_out_index` 表示“当前交易输出索引”，输入引用上一笔输出时使用 `prev_tx_out_index`，比单独的 `vout` 更适合长期维护和跨链统一建模。

```sql
CREATE TABLE `wallet_utxos` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY,
    `chain` VARCHAR(20) NOT NULL COMMENT 'BTC',
    `txid` VARCHAR(64) NOT NULL COMMENT '产生该 UTXO 的交易 hash',
    `tx_out_index` INT UNSIGNED NOT NULL COMMENT '交易输出索引，对应链上 vout/n',
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
    UNIQUE KEY `uk_outpoint` (`chain`, `txid`, `tx_out_index`),
    KEY `idx_address_status` (`chain`, `address`, `status`),
    KEY `idx_status_amount` (`chain`, `status`, `amount`),
    KEY `idx_spend` (`chain`, `spend_txid`),
    KEY `idx_height` (`chain`, `block_height`)
) ENGINE=InnoDB COMMENT='BTC UTXO 状态表';
```

交易索引表：

```sql
CREATE TABLE `wallet_btc_txs` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '自增主键',
    `chain` VARCHAR(20) NOT NULL COMMENT '链标识，BTC/mainnet/testnet 可按项目规范拆分',
    `txid` VARCHAR(64) NOT NULL COMMENT '交易 txid，非 witness 数据计算得到的交易哈希',
    `wtxid` VARCHAR(64) NOT NULL DEFAULT '' COMMENT 'witness txid，SegWit 交易包含 witness 数据的哈希，非 SegWit 可为空或等于 txid',
    `block_height` BIGINT NOT NULL DEFAULT 0 COMMENT '交易所在区块高度，0=未确认',
    `block_hash` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '交易所在区块 hash，未确认为空',
    `tx_index` INT UNSIGNED NOT NULL DEFAULT 0 COMMENT '交易在区块内的序号，用于稳定排序和回滚重放',
    `version` INT NOT NULL COMMENT 'BTC 交易版本号',
    `lock_time` BIGINT NOT NULL DEFAULT 0 COMMENT '交易 locktime，可能表示区块高度或时间戳',
    `size` INT UNSIGNED NOT NULL DEFAULT 0 COMMENT '交易原始字节大小，单位 byte',
    `vsize` INT UNSIGNED NOT NULL DEFAULT 0 COMMENT '交易虚拟大小，单位 vB，用于手续费率计算',
    `weight` INT UNSIGNED NOT NULL DEFAULT 0 COMMENT '交易 weight，SegWit 权重单位',
    `fee` VARCHAR(40) NOT NULL DEFAULT '0' COMMENT '手续费，单位 satoshi，coinbase 或未知输入交易可为0',
    `direction` TINYINT NOT NULL DEFAULT 0 COMMENT '0=External 1=Inbound 2=Outbound 3=Sweep 4=InternalMixed',
    `status` TINYINT NOT NULL DEFAULT 0 COMMENT '0=Unconfirmed 1=Confirmed 2=Reverted',
    `btime` DATETIME NULL COMMENT '区块时间，未确认为空',
    `ctime` DATETIME NOT NULL COMMENT '创建时间',
    `mtime` DATETIME NOT NULL COMMENT '更新时间',
    UNIQUE KEY `uk_txid` (`chain`, `txid`),
    KEY `idx_height` (`chain`, `block_height`),
    KEY `idx_status` (`chain`, `status`)
) ENGINE=InnoDB COMMENT='BTC 交易索引表';
```

输入明细表用于审计和回滚：

```sql
CREATE TABLE `wallet_btc_tx_inputs` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '自增主键',
    `chain` VARCHAR(20) NOT NULL COMMENT '链标识，BTC/mainnet/testnet 可按项目规范拆分',
    `txid` VARCHAR(64) NOT NULL COMMENT '当前交易 txid，即花费上一笔输出的交易',
    `vin_index` INT UNSIGNED NOT NULL COMMENT '当前交易输入索引，对应链上 vin 数组下标',
    `prev_txid` VARCHAR(64) NOT NULL COMMENT '被花费 UTXO 的产生交易 txid',
    `prev_tx_out_index` INT UNSIGNED NOT NULL COMMENT '被花费交易输出索引，对应 previous outpoint vout',
    `prev_address` VARCHAR(120) NOT NULL DEFAULT '' COMMENT '被花费输出地址，外部输入可能无法可靠解析',
    `prev_amount` VARCHAR(40) NOT NULL DEFAULT '0' COMMENT '被花费输出金额，单位 satoshi；SegWit 验签和手续费核算需要',
    `script_sig` TEXT COMMENT '输入 scriptSig hex，SegWit P2WPKH 通常为空',
    `sequence` BIGINT UNSIGNED NOT NULL COMMENT '输入 sequence，RBF/CSV/locktime 相关',
    `witness` MEDIUMTEXT COMMENT 'witness 数据 hex 或序列化 JSON，非 SegWit 可为空',
    `is_ours` TINYINT NOT NULL DEFAULT 0 COMMENT '是否花费本系统 UTXO，0=否 1=是',
    `block_height` BIGINT NOT NULL DEFAULT 0 COMMENT '当前交易所在区块高度，0=未确认',
    `ctime` DATETIME NOT NULL COMMENT '创建时间',
    UNIQUE KEY `uk_input` (`chain`, `txid`, `vin_index`),
    KEY `idx_prevout` (`chain`, `prev_txid`, `prev_tx_out_index`),
    KEY `idx_height` (`chain`, `block_height`)
) ENGINE=InnoDB COMMENT='BTC 交易输入明细';
```

输出明细表用于完整审计；如果只做教学 demo，可先只保留 `wallet_utxos`：

```sql
CREATE TABLE `wallet_btc_tx_outputs` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '自增主键',
    `chain` VARCHAR(20) NOT NULL COMMENT '链标识，BTC/mainnet/testnet 可按项目规范拆分',
    `txid` VARCHAR(64) NOT NULL COMMENT '产生该输出的交易 txid',
    `tx_out_index` INT UNSIGNED NOT NULL COMMENT '交易输出索引，对应链上 vout/n',
    `address` VARCHAR(120) NOT NULL DEFAULT '' COMMENT '输出地址，OP_RETURN/复杂脚本可能为空',
    `amount` VARCHAR(40) NOT NULL COMMENT '输出金额，单位 satoshi',
    `script_pubkey` TEXT NOT NULL COMMENT '输出锁定脚本 scriptPubKey hex',
    `script_type` VARCHAR(32) NOT NULL DEFAULT '' COMMENT '脚本类型，如 witness_v0_keyhash/pubkeyhash/scripthash/witness_v1_taproot/nulldata',
    `is_ours` TINYINT NOT NULL DEFAULT 0 COMMENT '是否命中本系统地址，0=否 1=是',
    `block_height` BIGINT NOT NULL DEFAULT 0 COMMENT '交易所在区块高度，0=未确认',
    `ctime` DATETIME NOT NULL COMMENT '创建时间',
    UNIQUE KEY `uk_output` (`chain`, `txid`, `tx_out_index`),
    KEY `idx_address` (`chain`, `address`),
    KEY `idx_height` (`chain`, `block_height`)
) ENGINE=InnoDB COMMENT='BTC 交易输出明细';
```

### 12.2 四张表的业务关系与流转

四张核心表的职责：

```text
wallet_btc_txs         记录交易整体，与系统地址相关的交易
wallet_btc_tx_inputs   花费输入明细，记录“这笔交易花了哪些 UTXO”，非系统地址不记录
wallet_btc_tx_outputs  交易输出明细，记录“这笔交易产生了哪些输出”，包含了系统地址和用户的提现地址
wallet_utxos           记录系统控制的utxo，包含未花费和已花费
```

关系模型：

```text
wallet_btc_txs.txid
  -> wallet_btc_tx_inputs.txid
  -> wallet_btc_tx_outputs.txid

wallet_btc_tx_outputs(txid, tx_out_index)
  -> wallet_utxos(txid, tx_out_index)        仅 is_ours=1 的输出进入 UTXO 管理

wallet_btc_tx_inputs(prev_txid, prev_tx_out_index)
  -> wallet_utxos(txid, tx_out_index)        输入花费系统 UTXO 时命中
```

一句话概括：输出产生 UTXO，输入花费 UTXO。`wallet_btc_tx_outputs` 是完整交易输出审计表，`wallet_utxos` 是其中“命中系统地址、需要进入钱包状态机”的子集。

#### 用户充值流转

用户从外部钱包充值到交易所地址时，链上交易通常表现为：

```text
tx_deposit_1
  vout[0] -> 用户充值地址 A，0.1 BTC
  vout[1] -> 外部找零地址
```

扫块处理：

1. 插入 `wallet_btc_txs`，记录交易整体。

```text
txid=tx_deposit_1
direction=Inbound
status=Unconfirmed/Confirmed
block_height=...
```

2. 插入 `wallet_btc_tx_inputs`，记录外部输入，用于审计；通常 `is_ours=0`。
3. 插入 `wallet_btc_tx_outputs`，完整记录所有输出。命中系统地址的输出标记 `is_ours=1`。

```text
txid=tx_deposit_1
tx_out_index=0
address=用户充值地址 A
amount=10000000
is_ours=1
```

4. 对 `is_ours=1` 的输出插入 `wallet_utxos`。

```text
txid=tx_deposit_1
tx_out_index=0
address=用户充值地址 A
uid=用户ID
amount=10000000
status=Available
```

5. 充值单状态按确认数和风控流转：

```text
Detected -> PendingConfirm -> Credited
```

注意：用户余额上账不等于 UTXO 已归集。充值 UTXO 仍可能停留在用户充值地址上，后续再由归集任务花费并转入热钱包或冷钱包。

#### 归集流转

归集是把用户充值地址上的系统 UTXO 转成热钱包/冷钱包 UTXO。示例：

```text
input:
  prev_txid=tx_deposit_1
  prev_tx_out_index=0

output:
  tx_out_index=0 -> 热钱包地址 H
```

状态流转：

1. 选中 `wallet_utxos(tx_deposit_1, 0)`，在 DB 事务内加锁。

```text
Available -> Reserved
```

2. 构建、签名、广播归集交易后：

```text
Reserved -> PendingSpend
```

3. 归集交易确认后，原 UTXO 被花费：

```text
PendingSpend -> Spent
spend_txid=tx_sweep_1
```

4. 归集交易的新输出命中热钱包地址，插入新的 `wallet_utxos`：

```text
txid=tx_sweep_1
tx_out_index=0
address=热钱包地址 H
amount=归集金额 - fee
status=Available
```

归集本质是：

```text
用户地址 UTXO -> 热钱包/冷钱包 UTXO
```

#### 用户提现流转

用户提现通常花费热钱包或归集后的系统 UTXO。示例：

```text
input:
  prev_txid=tx_sweep_1
  prev_tx_out_index=0

outputs:
  tx_out_index=0 -> 用户外部地址 X
  tx_out_index=1 -> 系统找零地址 C
```

处理流程：

1. 从 `wallet_utxos` 选择系统可用 UTXO。

```text
status=Available
```

2. 选币事务内锁定：

```text
Available -> Reserved
reserved_by=提现单ID
```

3. 构建、签名、广播 rawtx 后：

```text
Reserved -> PendingSpend
spend_txid=tx_withdraw_1
```

4. 扫到提现交易后，插入 `wallet_btc_txs`。

```text
txid=tx_withdraw_1
direction=Outbound
fee=...
status=Confirmed
```

5. 插入 `wallet_btc_tx_inputs`，记录本次提现花费了哪个系统 UTXO。

```text
txid=tx_withdraw_1
vin_index=0
prev_txid=tx_sweep_1
prev_tx_out_index=0
is_ours=1
```

6. 插入 `wallet_btc_tx_outputs`。用户外部输出 `is_ours=0`，系统找零输出 `is_ours=1`。
7. 原 UTXO 更新为已花费：

```text
PendingSpend -> Spent
spend_txid=tx_withdraw_1
```

8. 找零输出生成新的系统 UTXO：

```text
txid=tx_withdraw_1
tx_out_index=1
address=系统找零地址 C
amount=change_sat
status=Available
```

提现单状态建议：

```text
Created -> Reserved -> Signed -> Broadcast -> Confirmed
```

失败处理：

- 签名前失败：`Reserved -> Available`。
- 广播失败且确认未进入 mempool：`Reserved -> Available`。
- 已进入 mempool：保持 `PendingSpend`，不能随便释放，必须通过节点、mempool、RBF/双花检测确认后再处理。
- 交易被 RBF 替换或所在区块回滚：按链上最终交易和本地重扫结果恢复 UTXO 状态。

### 12.3 UTXO 状态机

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
tx_out_index=0
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
tx_out_index=0
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
tx_out_index=1
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
    `prev_tx_out_index` INT UNSIGNED NOT NULL COMMENT '被花费交易输出索引，对应 previous outpoint vout',
    `amount` VARCHAR(40) NOT NULL,
    `address` VARCHAR(120) NOT NULL,
    `status` TINYINT NOT NULL DEFAULT 0 COMMENT '0=Reserved 1=Signed 2=Broadcast 3=Confirmed 4=Released',
    `ctime` DATETIME NOT NULL,
    `mtime` DATETIME NOT NULL,
    UNIQUE KEY `uk_business_input` (`chain`, `business_type`, `business_id`, `prev_txid`, `prev_tx_out_index`),
    KEY `idx_prevout` (`chain`, `prev_txid`, `prev_tx_out_index`)
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

建议围绕 `08-btc-manager/tx-sign-send/` 和 `08-btc-manager/block-tx-parser/` 分阶段实现：

1. 地址生成：在 `tx-sign-send` 中完善 BIP84 P2WPKH 地址派生和地址归属表。
2. 区块解析：在 `block-tx-parser` 中解析 mock/真实 BTC block，生成 inbound 和 UTXO。
3. UTXO 回滚：模拟 2 块重组，验证 `Spent -> Available` 和新 UTXO 删除。
4. 选币和手续费：在 `tx-sign-send` 中给定提现金额和 fee rate，选择 UTXO、生成找零。
5. 离线签名：在 `tx-sign-send` 中完成 P2WPKH rawtx 构建、签名、验签。
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

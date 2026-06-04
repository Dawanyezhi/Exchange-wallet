# Aptos 链接入调研

本文按交易所托管钱包视角调研 Aptos 接入：地址生成、离线签名、交易构建、充值提现归集、APT 与 Token 解析、REST/Indexer、节点部署、费用模型、finality/回滚风险和数据库设计。

Aptos 是 Move 语言驱动的 PoS BFT 公链。它不是 EVM 链，也不是 UTXO 链；钱包主流程应按 Account + Sequence Number 管理，但资产表达依赖 Move resource、Coin/Fungible Asset/Object 模型，不能只把交易解析简化成 `from/to/value`。

## 1. 基础结论

| 维度 | Aptos 结论 | 钱包影响 |
|------|------------|----------|
| 链类型 | Account 型，账户有 sequence number；资产存储在 Move resource / object 中 | 提现并发靠账户 sequence 锁；充值解析要按交易版本、事件和余额变更处理 |
| 共识/finality | PoS + AptosBFT；提交后是 BFT 确定性 finality | 业务上不需要 PoW 式多块确认，但仍要处理节点延迟、Indexer 延迟和异常暂停 |
| 原生币精度 | APT 使用 octa，`1 APT = 100,000,000 octas`，decimals=8 | 所有金额使用整数 octa，禁止 `float64` |
| 地址体系 | 32 字节账户地址，通常 `0x` + 64 位十六进制；短地址可补零 | 地址字段不能沿用 EVM 固定 42 长度；建议统一存规范化 66 字符地址 |
| 签名算法 | 默认 Ed25519；官方也支持 Secp256k1、Multisig、MultiKey 等认证方案 | 首期建议只支持 Ed25519 单签托管地址；keyman 需支持 SLIP-0010/Ed25519 |
| 交易模型 | `RawTransaction` 包含 sender、sequence、payload、gas、expiration、chain id | 防重放依赖 chain id + sequence + expiration；必须签后反解析 BCS raw tx |
| Token | 历史 Coin 标准、Fungible Asset 标准、Digital Asset/NFT 标准并存 | 首期建议 APT + 白名单 Coin/FA；NFT、复杂 object 资产延后 |
| Memo/Tag | 协议层没有强制 memo/tag | 建议每用户独立地址；共享地址 + memo 不是首期推荐方案 |
| RPC 能力 | Fullnode REST 支持账户、交易、区块、事件、模拟、估 gas；官方提供 Indexer 能力 | 生产入账建议自建 fullnode + indexer/stream，公共节点只做兜底 |
| 费用模型 | gas 使用 APT 支付，`fee = gas_used * gas_unit_price`，单位 octa | 提现前必须 simulate/估 gas，设置 `max_gas_amount` 和手续费上限 |
| 生产风险 | sequence 卡单、过期交易、Move abort、Token 标准迁移、Indexer 延迟、地址短格式混乱 | 需要 nonce/sequence 状态机、交易补偿扫描、资产白名单和严格验址 |

## 2. 官方资料确认的事实

官方文档说明，Aptos 账户由 32 字节 account address 标识，通常以 64 个十六进制字符展示，也允许 `0x1` 这类短地址形式；账户可以显式创建，也可以通过接收 APT 被隐式创建。账户的 sequence number 表示该账户已提交并上链的交易序号，每笔交易必须携带唯一 sequence，链上会用它防止旧交易重放并约束交易顺序。Aptos 默认使用 Ed25519 签名交易，Ed25519 地址的认证 key 由 `sha3-256(pubkey || 0x00)` 派生。

官方 REST API 文档说明，用户交易包含 `sender`、`sequence_number`、`max_gas_amount`、`gas_unit_price`、`expiration_timestamp_secs`、`payload`、`signature` 等字段；`u64` 在 JSON 中以字符串表达，避免 JavaScript 等语言的整数精度问题。REST 还提供 `/transactions/encode_submission`、`/transactions/simulate`、`/estimate_gas_price`、按 hash/version/account 查询交易、按 height/version 查询 block 等接口。

官方节点文档说明，Aptos 节点分 validator node 和 fullnode。Fullnode 不参与共识，但会同步链上状态并提供 REST 服务；第三方浏览器、钱包、交易所和 dapp 可以运行本地 fullnode，以获得一致视图、避免公共节点限流，并运行自定义历史分析。

官方标准文档说明，Aptos 资产标准至少包括 Coin、Fungible Asset 和 Digital Asset。Coin 是历史轻量 fungible token 标准，`CoinStore<T>` 持有余额和 deposit/withdraw event；Fungible Asset 是基于 object model 的新 fungible asset 标准，旨在覆盖更通用的可替代资产；Digital Asset 是 NFT 标准。

参考资料：

- Aptos Accounts: <https://legacy.aptos.dev/concepts/accounts/>
- Aptos REST API: <https://aptos.dev/rest-api>
- Submit transaction: <https://aptos.dev/rest-api/operations/submit_transaction>
- Simulate transaction: <https://aptos.dev/rest-api/operations/simulate_transaction>
- Encode submission: <https://aptos.dev/rest-api/operations/encode_submission>
- Estimate gas price: <https://aptos.dev/rest-api/operations/estimate_gas_price>
- Fullnodes overview: <https://legacy.aptos.dev/concepts/fullnodes/>
- Aptos Coin: <https://legacy.aptos.dev/standards/aptos-coin/>
- Fungible Asset: <https://legacy.aptos.dev/standards/fungible-asset/>
- Aptos Go SDK: <https://github.com/aptos-labs/aptos-go-sdk>
- Aptos TypeScript SDK transaction builder: <https://legacy.aptos.dev/sdks/ts-sdk/transaction-builder/>

## 3. 链基础信息

官方资料确认：

- 主网：Aptos Mainnet。
- 测试网络：Testnet、Devnet、Localnet。Devnet 用于快速开发，历史上存在重置和频繁升级特征；Testnet 更适合稳定集成测试。
- 原生币：APT。
- 最小单位：octa。
- 精度：8，`1 APT = 100000000 octas`。
- 主流 RPC 形态：Fullnode REST API，主网公共入口示例为 `https://api.mainnet.aptoslabs.com/v1/`。
- 主网 chain id 可通过 REST ledger info 或响应头确认；生产系统不应硬编码到不可升级配置里，启动时应校验 RPC 返回的 chain id 与本地配置一致。

工程建议：

- `coinset` 配置中显式记录 `chain_id=1` 作为主网预期值，同时每次启动和广播前用 `/v1/` 或响应头 `X-APTOS-CHAIN-ID` 校验。
- 测试网、Devnet 使用独立 chain code、数据库命名空间和签名 key id，禁止主网私钥在测试环境出现。
- 链上金额字段统一使用 decimal string 或 `uint64` octa。Go 代码内部可用 `uint64` 表示 APT 和大多数 Coin/FA 的 `u64` amount；统一多链金额层仍建议用 `internal/bigint`，避免未来跨链资产或统计聚合溢出。

## 4. 账户模型和钱包影响

Aptos 是 Account 模型，但与 EVM 的关键差异是：

- Aptos 没有 EVM nonce 字段名，使用 account `sequence_number`。
- 交易 payload 是 Move entry function、script 或 module publish，不是 EVM calldata。
- 链上资产是 Move resource/object，不是统一的 `balanceOf` 合约查询。
- 交易上链失败也可能被 committed 为 abort 状态，交易会消耗 gas，但业务资产状态不变。
- 交易历史有全局递增 `version`，比 block 内 index 更适合作为入账游标。

对业务闭环的影响：

| 流程 | 设计要点 |
|------|----------|
| 充值 | 以交易 `version` 为全局游标，解析 success 交易的 events / balance changes，按 `chain + version + event_index + asset_id` 去重 |
| 提现 | 每个热钱包 sender 维护 sequence 锁；同一 sequence 同时只能绑定一个有效 raw tx |
| 归集 | 多个充值地址归集到热/冷钱包时，每个 from 地址独立 sequence，可并发；同一 from 内串行 |
| 对账 | APT 查 CoinStore/余额；Coin/FA/NFT 按白名单资源或 Indexer 资产表核对 |
| 回滚 | BFT 提交后不按 PoW 深度回滚，但本地索引重放、节点落后、误解析和失败补偿仍要可恢复 |

## 5. 地址体系

官方资料确认：

- account address 是 32 字节十六进制地址。
- 地址常见展示为 `0x` + 64 hex。
- 特殊地址如 `0x1` 可以省略前导零；普通业务地址建议规范化为完整 32 字节形式。
- 初始地址通常等于 authentication key；Aptos 支持 key rotation，因此“当前认证 key”和“account address”不是永远等价。

工程建议：

- 入库前规范化：小写、`0x` 前缀、完整 64 hex；仅系统模块地址允许展示短地址，但入库仍补齐。
- 数据库字段建议 `VARCHAR(80)` 起步；如果统一多链地址表，建议 `VARCHAR(160)`。
- 地址校验不能只判断 `0x` 开头，应解析十六进制并确认补零后恰好 32 字节。
- 首期不支持 ANS 域名作为提现目标，只允许解析后的 account address。
- key rotation 会导致同一 account address 后续可能由不同 public key 控制。交易所托管地址不应允许用户侧 rotate，热/冷钱包如需 rotate 必须纳入 keyman 审批、审计和账户清单变更。

## 6. 密码学算法和派生

官方资料确认：

- Aptos 默认 Ed25519 交易签名。
- Ed25519 认证 key：`sha3-256(pubkey || 0x00)`，初始 account address 使用该 authentication key。
- 官方资料也说明支持 Secp256k1 ECDSA、K-of-N 多签、legacy MultiEd25519 和 generalized authentication。

工程建议：

- 首期只支持 Ed25519 单签托管地址，避免 Secp256k1/generalized/multikey 的地址和 authenticator 分支扩大验签面。
- 派生路径建议遵循 SLIP-0044 coin type 637：

```text
m/44'/637'/account'/change'/address_index'
```

- 由于 Ed25519 派生通常使用硬化派生，不能直接复用 EVM BIP32 secp256k1 派生代码。
- 签名服务不能只签调用方传入的 32 字节 hash。keyman 应接收完整 Aptos RawTransaction 结构或 canonical BCS bytes，并独立重算 signing message。
- 签名审计记录至少包括：`chain`、`network`、`chain_id`、sender、sequence、payload function、type arguments、arguments 摘要、max gas、gas price、expiration、业务单号、key id、public key、签名结果。

## 7. 交易构建与签名

Aptos 普通单签交易的核心结构：

```text
RawTransaction {
  sender: AccountAddress,
  sequence_number: u64,
  payload: TransactionPayload,
  max_gas_amount: u64,
  gas_unit_price: u64,
  expiration_timestamp_secs: u64,
  chain_id: u8
}

Authenticator {
  public_key,
  signature
}
```

防重放机制：

- `chain_id` 限制交易只能在目标 Aptos 网络执行。
- `sequence_number` 限制同一 sender 的交易顺序，旧 sequence 不可重放。
- `expiration_timestamp_secs` 限制交易在链上时间超过后被丢弃。

签名前必须校验：

- sender 是系统热钱包、归集地址或指定托管地址。
- sequence 来自可信 fullnode 和本地 sequence 锁，不允许业务请求方传入。
- payload function 在白名单中，例如 APT 提现只允许 `0x1::aptos_account::transfer` 或明确批准的 `transfer_coins`。
- Token 类型、FA metadata/object 地址在资产白名单中。
- amount 是最小单位整数字符串，不能出现小数或科学计数法。
- `max_gas_amount * gas_unit_price` 不超过币种和业务阈值。
- expiration 合理，避免太短导致广播即过期，也避免太长造成 sequence 长时间占用。

签后必须反解析：

- 从 signed transaction BCS 中解出 RawTransaction 和 Authenticator。
- 本地验证 Ed25519 签名。
- 核对 sender、sequence、chain id、gas、expiration、payload function、type args、arguments。
- 计算交易 hash，并将 raw tx、hash、sequence、业务单号、签名审计落库。

### 7.1 生产案例：APT 主币提现构建与离线签名

场景：热钱包向外部用户地址提现 `2 APT`。金额以 octa 表示，`2 APT = 200000000 octas`。

业务请求：

```json
{
  "chain": "APTOS",
  "network": "mainnet",
  "business_type": "withdraw",
  "business_id": 10010001,
  "asset": {
    "symbol": "APT",
    "asset_type": "native",
    "decimals": 8
  },
  "from_address": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "to_address": "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "amount_octa": "200000000",
  "fee_policy": {
    "max_gas_amount": "2000",
    "max_gas_unit_price": "200",
    "max_fee_octa": "400000"
  }
}
```

链上状态输入由钱包服务从可信节点和本地库读取：

```json
{
  "ledger": {
    "chain_id": 1,
    "ledger_version": "1234567890",
    "ledger_timestamp_usec": "1760000000000000"
  },
  "sender_account": {
    "address": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "onchain_sequence_number": "77",
    "local_next_sequence": "77",
    "available_balance_octa": "10000000000"
  },
  "gas_estimate": {
    "gas_unit_price": "100"
  },
  "recipient": {
    "address_normalized": "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    "account_may_not_exist": true
  }
}
```

sequence 锁定：

```text
BEGIN;
SELECT * FROM aptos_account_sequences
 WHERE chain='APTOS' AND network='mainnet' AND address=:from
 FOR UPDATE;

校验 local_next_sequence == onchain_sequence_number 或处于可恢复状态；
INSERT INTO aptos_sequence_locks(..., sequence_number=77, business_id=10010001, status='Locked');
UPDATE aptos_account_sequences SET local_next_sequence=78 WHERE address=:from;
COMMIT;
```

unsigned RawTransaction：

```json
{
  "sender": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "sequence_number": "77",
  "payload": {
    "type": "entry_function_payload",
    "function": "0x1::aptos_account::transfer",
    "type_arguments": [],
    "arguments": [
      "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      "200000000"
    ]
  },
  "max_gas_amount": "2000",
  "gas_unit_price": "100",
  "expiration_timestamp_secs": "1760000600",
  "chain_id": 1
}
```

使用 `0x1::aptos_account::transfer` 的原因：APT 提现到尚未创建的账户时，该入口可覆盖隐式创建账户路径。若改用泛型 Coin 转账，例如 `0x1::coin::transfer<0x1::aptos_coin::AptosCoin>`，收款账户未注册对应 `CoinStore` 时可能失败。生产中必须固定入口函数白名单，并在文档里说明两者差异。

签名前策略校验：

- `from_address` 必须属于系统热钱包，且 key id 状态为 active。
- `to_address` 必须通过风控地址校验，不在制裁/黑名单/内部冻结名单。
- amount、fee、gas 均为十进制整数；`amount_octa > 0`。
- `max_gas_amount * gas_unit_price = 200000 octas`，小于 `max_fee_octa=400000`。
- `available_balance_octa >= amount_octa + max_fee_octa`。
- sequence `77` 已本地锁定给业务单 `10010001`。
- payload 中没有额外 Move function、multi-agent、fee payer、module publish 或 script payload。
- chain id 与 mainnet 配置一致。

签名服务请求不要只传 hash，建议传完整结构：

```json
{
  "request_id": "aptos-sign-10010001-77",
  "chain": "APTOS",
  "network": "mainnet",
  "key_id": "aptos-hot-001",
  "derivation_path": "m/44'/637'/0'/0'/12'",
  "expected_public_key": "0x02...ed25519-pubkey-32-bytes...",
  "expected_sender": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "raw_transaction": {
    "sender": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "sequence_number": "77",
    "payload": {
      "type": "entry_function_payload",
      "function": "0x1::aptos_account::transfer",
      "type_arguments": [],
      "arguments": [
        "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
        "200000000"
      ]
    },
    "max_gas_amount": "2000",
    "gas_unit_price": "100",
    "expiration_timestamp_secs": "1760000600",
    "chain_id": 1
  },
  "policy": {
    "business_id": "10010001",
    "allowed_functions": ["0x1::aptos_account::transfer"],
    "max_fee_octa": "400000",
    "asset": "APT",
    "amount_octa": "200000000"
  }
}
```

签名服务处理：

1. 用 key id/派生路径找到 Ed25519 私钥。
2. 从 public key 重算 authentication key，确认派生出的 sender 与请求 `expected_sender` 一致。
3. 按 Aptos 规范对 RawTransaction 做 BCS 序列化并构造 signing message。
4. Ed25519 签名 signing message。
5. 返回 public key、signature、signed transaction BCS 或可组装的 authenticator。

签名响应：

```json
{
  "request_id": "aptos-sign-10010001-77",
  "signer_address": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "public_key": "0x02...ed25519-pubkey-32-bytes...",
  "signature": "0x...64-byte-ed25519-signature...",
  "signed_txn_bcs": "0x...bcs-signed-transaction...",
  "signing_message_digest": "0x...sha3-256-for-audit-only..."
}
```

签后校验与广播：

```text
1. 反解析 signed_txn_bcs，得到 RawTransaction + Authenticator。
2. 本地验证 Ed25519 signature。
3. 核对 sender、sequence=77、chain_id=1、payload、amount、gas、expiration。
4. 调用 /transactions/simulate 做预执行；只接受 success=true 且 gas_used 在阈值内。
5. 调用 POST /transactions 提交 BCS signed transaction。
6. 记录 tx_hash、rawtx、sequence、expiration、submit ledger version。
7. wait by hash 或扫交易 version 补偿状态。
```

落库状态机：

| 表/对象 | 关键字段 |
|---------|----------|
| `withdraw_orders` | `business_id`、`chain`、`asset_id`、`amount`、`to_address`、`status` |
| `aptos_account_sequences` | `address`、`onchain_sequence`、`local_next_sequence`、`sync_status` |
| `aptos_sequence_locks` | `address`、`sequence_number`、`business_id`、`status`、`expires_at` |
| `chain_raw_transactions` | `tx_hash`、`raw_tx`、`sender`、`sequence`、`payload_hash`、`broadcast_status` |
| `sign_audit_logs` | `request_id`、`key_id`、`policy_snapshot`、`public_key`、`signature`、`decision` |

失败和重发处理：

- 广播前校验失败：释放 sequence lock，提现单回到待构建或失败。
- simulate abort：记录 VM status，不广播，释放 sequence lock；如果是余额不足/账户状态异常，转人工。
- submit 返回 sequence too old：立即查链上交易和 account sequence，若该 sequence 已被其他交易占用，暂停该 sender 并人工核对。
- submit 返回 sequence too new：本地 sequence 领先链上，保留 lock，进入延迟重试或等待前序交易上链。
- expiration 过期未上链：旧 raw tx 不可再用；释放或标记 expired 后用新的当前 sequence 重新构建重签。若链上 sequence 已前进，必须先找到占用该 sequence 的交易。
- Aptos 没有 EVM replacement by gas price 语义。sequence 卡住时通常需要等待、确认过期、或提交同 sequence 的可接受交易；生产实现要先以官方节点/mempool 行为实测后再开放“取消/替换”。

## 8. Token、NFT、Memo/Tag

### 8.1 APT 和 Coin 标准

APT 是原生 gas coin，历史上按 `0x1::aptos_coin::AptosCoin` 作为 Coin type 表示。Coin 标准中，用户余额存储在 `CoinStore<CoinType>` resource，包含余额、冻结标志以及 deposit/withdraw event handle。

工程建议：

- APT 充值可从交易事件、余额变更、账户资源三者交叉验证。
- 首期支持非 APT Coin 时，资产白名单必须记录完整 `CoinType`，例如 `{address}::{module}::{struct}`，不能只用 symbol。
- `decimals` 从链上 metadata 或可信配置读取后写入资产配置；不允许提现请求传 decimals。
- 如果目标地址未注册对应 CoinStore，普通 Coin transfer 可能失败；提现前需要检查注册状态，或使用官方推荐的入口函数/初始化流程。

### 8.2 Fungible Asset 标准

Fungible Asset 是基于 object model 的资产标准，用 metadata object 区分资产，余额存在 `FungibleStore` 中，可产生 Deposit/Withdraw/Frozen 等事件。

工程建议：

- 首期如果支持 FA，资产白名单应记录 metadata object address、decimals、symbol、可转账状态、是否可冻结。
- 入账唯一键建议为 `chain + tx_version + event_index + asset_id + owner/store`。
- 需要 Indexer 或自建处理器解析 `FungibleStore` 归属关系和 primary store；只查账户资源容易漏掉 object store 情况。
- 对带 freeze/transfer ref 管理能力的资产，要在风控中标记冻结、强制转移、暂停等管理风险。

### 8.3 NFT/Digital Asset

首期不建议支持 NFT/Digital Asset 充值、提现和归集。原因：

- Digital Asset 基于 object model，所有权、collection、token metadata 和 transfer 事件解析复杂。
- 误把 NFT object 当 fungible token 处理可能导致错账。
- NFT 需要独立展示、定价、风控和防误转流程。

### 8.4 Memo/Tag

Aptos 协议层没有强制 memo/tag。工程建议：

- 交易所首期为每个用户分配独立 Aptos 地址，不使用共享地址 + memo。
- 如果未来为了降低地址管理成本采用共享地址 + memo，memo 必须是业务系统 payload 或自定义 Move 调用的一部分，签名前后都要反解析校验；普通 APT transfer 本身不提供统一 memo 字段。

## 9. RPC、节点和索引器

官方 REST 能力：

| 能力 | 接口示例 | 钱包用途 |
|------|----------|----------|
| Ledger info | `GET /v1/` | 获取 chain id、ledger version、block height、节点角色 |
| Account | `GET /accounts/{address}` | 获取 sequence、authentication key 等账户信息 |
| Balance | `GET /accounts/{address}/balance/{asset_type}` 或资源查询 | 查询 APT/资产余额，具体资产类型需按标准确认 |
| Transactions | `GET /transactions`、`GET /transactions/by_hash/{hash}`、`GET /transactions/by_version/{version}` | 扫链、补偿提现状态、对账 |
| Account transactions | `GET /accounts/{address}/transactions` | 小规模地址补偿；全量交易所入账不建议只依赖它 |
| Blocks | `GET /blocks/by_height/{height}`、`GET /blocks/by_version/{version}` | block 维度同步和监控 |
| Events | `GET /accounts/{address}/events/...` | Coin event handle 兼容解析 |
| Simulate | `POST /transactions/simulate` | 广播前预执行和 gas 估算 |
| Encode submission | `POST /transactions/encode_submission` | 将 JSON 交易转 BCS；生产不应盲信第三方节点编码 |
| Estimate gas | `GET /estimate_gas_price` | 估算 gas unit price |

工程建议：

- 生产至少部署自有 fullnode，并配置独立读节点和广播节点；公共 Aptos Labs endpoint 只作为健康对比和灾备。
- 充值入账不建议只轮询用户地址交易。Aptos 有全局 transaction version，交易所应按 version 递增扫交易，或使用官方/自建 Indexer/Transaction Stream。
- 自建节点必须关注 pruning。历史交易、事件和状态若被裁剪，会影响补偿对账；交易所需要保留足够历史或落本地索引。
- 对 REST 返回的交易要检查 `success`、`vm_status`、`version`、`timestamp`、events、changes。失败交易不能按 payload 入账。
- 多节点结果不一致时，以落后节点暂停读写，不做自动入账；触发 `ledger_version_gap` 告警。

## 10. 费用模型

官方 REST 文档说明，交易字段包含：

- `max_gas_amount`：本交易最多可消耗 gas unit 数。
- `gas_unit_price`：每 gas unit 支付多少 octa。
- `expiration_timestamp_secs`：交易过期时间。

实际手续费通常为：

```text
fee_octa = gas_used * gas_unit_price
```

费用估算例子：

```text
业务：提现 2 APT
amount_octa = 200000000
gas_unit_price = 100 octa
max_gas_amount = 2000
max_fee_octa = 2000 * 100 = 200000 octa = 0.002 APT
simulate gas_used = 600
actual_fee_octa = 600 * 100 = 60000 octa = 0.0006 APT
```

工程建议：

- 提现冻结金额按 `amount + max_fee` 冻结，成交后按 `actual_fee` 释放差额。
- gas price 使用 `/estimate_gas_price` 作为参考，但必须有本地上下限。
- 突发拥堵时允许使用 prioritized gas，但要受单笔、单地址、全局小时级手续费预算限制。
- 归集小额地址时，若 `balance <= estimated_fee + reserve`，不应归集，避免手续费吞噬余额。
- 对非 APT Token 提现，sender 必须额外持有 APT 支付 gas；热钱包 APT 余额不足应触发自动补 gas 或暂停 Token 提现。

## 11. Finality、异常和回滚

官方资料确认，Aptos 交易状态包括 committed executed 和 committed aborted；交易提交后可通过 hash/version 查询交易结果。Fullnode 会从上游同步交易和状态，但 fullnode 可能存在延迟，节点存储还可能因 pruning 影响历史查询。

工程建议：

- Aptos 不是 PoW 概率确认链，充值确认数不应照搬 BTC 的 6 确认。业务可配置：
  - demo/小额：交易 `success=true` 且出现在可信节点 ledger 中即可展示待入账。
  - 普通充值：等待交易 version 被两个自有节点或自有节点 + 可信供应商确认，并等待本地风控扫描完成。
  - 大额充值：在上述基础上增加人工/风控延迟，例如等待若干秒或若干 ledger version，确认无节点分歧。
- 不设计常规“深度回滚”流程，但必须有“本地索引重放/撤销”能力：如果解析程序 bug、资产白名单错误或节点返回不一致，需要按 version 区间重放并修正充值、余额、对账快照。
- 关键告警：
  - 自有节点落后公共节点超过阈值。
  - Indexer 最新 version 落后 fullnode。
  - 同一交易 hash 多节点状态不一致。
  - 热钱包 sequence 本地值与链上值偏差。
  - 提现 sequence 长时间 pending 或 expired。
  - Move abort 率异常升高。

## 12. 充值设计

首期建议支持：

- APT 主币充值。
- 白名单 Coin/FA 充值。
- 每用户独立 Aptos 地址。

扫链流程：

1. `aptos_sync_cursor` 记录已处理 `ledger_version`。
2. 从 fullnode/Indexer/Transaction Stream 按 version 顺序读取交易。
3. 只处理 user transaction，且 `success=true`。
4. 对 APT 解析 `0x1::aptos_account::transfer`、CoinStore deposit events、balance changes。
5. 对 Coin/FA 按资产白名单解析 events/object changes。
6. 命中系统地址后写入 `deposits`，唯一键为 `chain + tx_hash/version + event_index + asset_id + to_address`。
7. 充值先进入 `Detected`，经过风控、确认策略和对账后变为 `Credited`。

假充值防护：

- 不能只看 payload 中的目标地址；必须确认交易 success 且事件/状态变更实际增加系统地址余额。
- 不能按 symbol 入账；必须匹配 CoinType 或 FA metadata object。
- 不能信任第三方浏览器 webhook；浏览器只做辅助。
- 对 abort 交易、模拟交易、pending 交易、错误链 testnet/devnet 交易一律不入账。

## 13. 提现和归集设计

提现：

- APT 提现使用热钱包 sender sequence 串行构建。
- Token 提现按资产标准选择 Coin/FA transfer function，严格白名单函数和 type args。
- 广播前必须 simulate；广播后按 hash 等待 committed 并检查 `success=true`。
- 成功交易按 `gas_used * gas_unit_price` 核算手续费。
- abort committed 交易要把业务置为链上失败，释放用户金额但扣除/核算 gas，具体账务策略需产品和财务确认。

归集：

- 每个充值地址作为 sender，自身 sequence 独立，可多地址并发。
- 小额归集需计算 gas 经济性。
- Token 归集前确认充值地址有足够 APT gas；不足时需要补 gas 交易，补 gas 本身也占用热钱包 sequence。
- 对长尾 Token、冻结资产、可疑地址资产不自动归集，先进入人工处理池。

## 14. 数据库表建议

在当前项目教学结构上，建议新增或扩展以下表/模型：

| 表名 | 用途 | 关键字段 |
|------|------|----------|
| `aptos_accounts` | 托管地址表 | `address`、`auth_key`、`pubkey`、`derivation_path`、`account_type`、`status` |
| `aptos_account_sequences` | sequence 管理 | `address`、`onchain_sequence`、`local_next_sequence`、`updated_version`、`status` |
| `aptos_sequence_locks` | 提现/归集并发锁 | `address`、`sequence_number`、`business_id`、`raw_tx_id`、`status`、`expires_at` |
| `aptos_sync_cursors` | 扫链游标 | `network`、`last_processed_version`、`last_block_height`、`source` |
| `aptos_transactions` | 链上交易索引 | `tx_hash`、`version`、`sender`、`sequence`、`success`、`vm_status`、`gas_used` |
| `aptos_events` | 资产事件索引 | `version`、`event_index`、`event_type`、`account`、`asset_id`、`amount` |
| `chain_assets` | 资产白名单 | `asset_id`、`standard`、`coin_type`、`metadata_object`、`decimals`、`status` |
| `chain_raw_transactions` | raw tx 审计 | `chain`、`raw_tx`、`tx_hash`、`sign_request_id`、`broadcast_status` |

回滚/重放要求：

- `deposits` 必须能按 `version/event_index` 定位并撤销。
- `system_balances` 需要按 `asset_id` 和链上 version 生成快照。
- `sequence_locks` 不能简单删除，失败、过期、释放、占用都要留审计。

## 15. 当前项目落地改造建议

`internal/coinset` 建议新增：

```go
{
    Chain: "APTOS",
    NativeSymbol: "APT",
    ChainType: "account_sequence",
    AddressEncoding: "aptos_hex_32",
    Decimals: 8,
    Features: {
        SupportsToken: true,
        SupportsMemo: false,
        SupportsNonce: false,
        SupportsSequence: true,
        SupportsFinalityByVersion: true,
        SupportsEd25519: true,
    },
}
```

keyman 改造：

- 新增 Ed25519 key generation、SLIP-0010 派生、Aptos address 派生。
- 新增 Aptos RawTransaction 签名 demo，不允许通用 blind hash signing。
- 签名机侧实现 payload 白名单和 gas/sequence/chain id 校验。

block sync 改造：

- 新增按 transaction version 扫链 demo。
- 记录 fullnode ledger version、block height 和 indexer version 差异。
- 失败交易、abort 交易、success 交易分别测试。

deposit 改造：

- APT deposit event/balance change 解析。
- CoinType/FA metadata 白名单匹配。
- 假充值测试：payload 伪造、abort 交易、错误 asset id、testnet 交易、重复 event。

withdrawal 改造：

- sequence lock 状态机。
- Aptos RawTransaction 构建、simulate、签名、反解析、广播 demo。
- sequence too old/too new、expiration、abort、余额不足测试。

sweep/reconciliation 改造：

- 多地址 sequence 并发归集。
- Token gas 不足补 gas。
- version 维度对账和重放。

## 16. 生产安全清单

- 私钥：主网 Ed25519 私钥只在 HSM/离线签名服务中，禁止导出。
- 地址：所有地址入库规范化为 32 字节 hex，禁止短地址和大小写混用导致重复账户。
- 金额：所有接口金额使用最小单位字符串；API 层拒绝小数。
- 资产：APT/Coin/FA 分标准建模；Token 白名单使用 CoinType 或 metadata object，不使用 symbol。
- 签名：签名服务必须重算 sender、signing message、payload、gas、chain id，不盲签 hash。
- Sequence：每个 sender 单独锁；卡单自动告警，不允许手工改库跳 sequence。
- 交易：广播前 simulate，广播后按 hash/version 确认 success。
- 失败：abort committed 交易独立账务处理，不能误判为提现成功。
- 节点：至少两个读源交叉校验；Indexer 落后时暂停自动入账。
- 对账：链上余额、内部总账、提现冻结、归集在途每日对账。
- 监控：节点落后、sequence 偏差、pending 超时、abort 率、gas 异常、资产白名单变更都要告警。

## 17. 接入结论

是否建议接入：建议接入，但首期只做 APT 主币和少量白名单 Coin/FA，不应一次性支持全部 Move 资产/NFT/复杂 account abstraction 能力。

最小上线能力：

- Ed25519 地址派生和验址。
- APT 充值、提现、归集。
- 按 transaction version 扫链和补偿。
- sequence lock 提现状态机。
- REST fullnode + Indexer/Transaction Stream 至少一种可恢复索引路径。
- APT 余额和手续费对账。

首期不建议支持：

- NFT/Digital Asset。
- Keyless accounts、account abstraction、multi-agent、sponsored transaction、orderless transaction。
- 用户自定义 Move payload 提现。
- 共享充值地址 + memo。
- 非白名单 Coin/FA 自动入账。

是否需要自建索引器：建议需要。小规模 demo 可用 REST 轮询；生产交易所为了历史补偿、事件解析、FA/object 归属和对账，应该自建 Indexer/Transaction Stream 或采购可靠供应商并落本地索引。

充值确认数如何配置：不按 PoW 确认数配置，建议配置为 `success=true + trusted ledger version observed + indexer caught up`。大额充值增加风控延迟和多节点一致性校验。

提现并发控制：靠 sender account sequence lock；同一 sender 严格串行，不同 sender 可并发。

回滚时恢复哪些表：`deposits`、`aptos_events`、`aptos_transactions`、`system_balances`、`reconciliation_snapshots`。提现 sequence 不做链回滚式恢复，但要能按链上 sequence 纠偏本地锁表。

对账口径：链上 `CoinStore`/FA balance 或 Indexer asset balance = 内部用户可用余额 + 冻结余额 + 系统归集在途 + 手续费储备 + 异常挂账。

上线前必须补的测试：

- 地址规范化和 Ed25519 地址派生测试。
- RawTransaction BCS 反解析和验签测试。
- sequence too old/too new/expired/abort 测试。
- APT 成功充值、失败交易不入账、重复 event 去重测试。
- CoinType/FA metadata 白名单测试。
- simulate gas 上限和实际手续费核算测试。
- 节点/Indexer 落后时暂停入账测试。

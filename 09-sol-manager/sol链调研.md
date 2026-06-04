# SOL 链接入调研

本文按交易所托管钱包视角调研 Solana 接入：地址生成、离线签名、交易构建、区块同步、充值提现归集、SPL Token、Memo、RPC 能力、节点部署、费用模型、finality/回滚风险和数据库设计。

Solana 不是 EVM Account 链，也不是 BTC UTXO 链。它是高性能账户模型链，但账户不仅表示“用户地址”，还表示可执行程序、Token Account、Associated Token Account、Nonce Account 等链上状态对象。交易所接入 SOL 主币可以按账户余额模型处理；接入 SPL Token 时必须理解 mint、token account、owner、ATA 和 Token-2022 扩展。

## 1. 基础结论

| 维度 | SOL 结论 | 钱包影响 |
|------|----------|----------|
| 链类型 | Account 型，但账户语义不同于 EVM | SOL 主币按地址余额处理；SPL Token 按 token account/ATA 解析 |
| 共识/finality | PoS + PoH + Tower BFT；RPC commitment 分 processed/confirmed/finalized | 充值应以 `finalized` 或足够 conservative 的 confirmed/finalized 策略入账 |
| 原生币精度 | 1 SOL = 1,000,000,000 lamports，decimals=9 | 全部金额使用 lamports 整数，禁止 `float64` |
| 地址体系 | ed25519 公钥 base58 编码，常见长度约 32-44 字符 | 地址字段不能沿用 EVM `VARCHAR(42)`，建议 `VARCHAR(64)` 或更宽 |
| 签名算法 | ed25519 | 不能复用 EVM secp256k1 签名；keyman 需支持 ed25519/SLIP-0010 |
| 交易模型 | 无 EVM Nonce；使用 recent blockhash 防重放，支持 durable nonce | 提现并发重点是签名地址和业务幂等，不是账户 Nonce |
| Token | SPL Token、Token-2022、ATA | Token 充值解析必须识别 mint、token account owner、decimals 和扩展特性 |
| Memo/Tag | Memo Program 可选；交易所可选择独立地址或共享地址+memo | 建议用户 SOL 主币独立地址；若 Token 使用共享 ATA/热地址则 memo 必须纳入归属 |
| 费用模型 | base fee 按签名数，默认每签名 5000 lamports；可加 priority fee | 手续费较低，但 Token 账户创建和 rent-exempt 余额需要单独核算 |
| RPC 能力 | 官方 JSON-RPC 支持余额、交易、区块和签名查询；长期历史依赖节点保留/BigTable/索引器 | 生产需自建 RPC 或可靠供应商，并做历史补偿和二次校验 |
| 生产风险 | 历史上发生过主网停机；高吞吐下 RPC 返回和历史查询容易受限 | 必须多节点、暂停策略、交易状态补偿和对账 |

## 2. 链基础信息

官方资料确认：

- 主网通常称 `mainnet-beta`。
- 测试网络包括 `devnet`、`testnet`，本地开发可用 `solana-test-validator`。
- 原生币为 SOL，最小单位为 lamport，`1 SOL = 10^9 lamports`。
- Solana 使用 Proof of History 辅助排序，并结合权益加权验证者投票达成共识。
- RPC commitment 常见级别包括 `processed`、`confirmed`、`finalized`。

工程建议：

- 钱包入账默认使用 `finalized` 查询结果，降低分叉和回滚处理复杂度。
- demo 可用 `confirmed` 展示更快确认，但文档必须说明与生产差距。
- 大额充值建议等 `finalized` 后再增加本地风控延迟或人工复核。

## 3. 账户模型

Solana 的账户是链上状态容器，每个账户至少有：

- `lamports`：SOL 余额。
- `owner`：拥有该账户数据解释权的 program id。
- `data`：账户数据。
- `executable`：是否可执行程序。
- rent 相关字段。

对交易所钱包最重要的账户类型：

| 账户类型 | 说明 | 钱包影响 |
|----------|------|----------|
| System Account | 普通 SOL 地址，由 System Program 管理 | 用户 SOL 充值地址、热钱包、冷钱包 |
| Token Mint | SPL Token 的 mint 定义 | Token 白名单必须以 mint 为准 |
| Token Account | 某个 owner 持有某个 mint 的余额账户 | SPL Token 充值入账的核心对象 |
| Associated Token Account | 由 wallet owner + mint 派生的标准 token account | 建议优先使用 ATA，减少归属歧义 |
| Nonce Account | Durable nonce 账户 | 大额/离线签名可考虑，但首期可不支持 |
| Program Account | 可执行程序 | 不应作为用户充值地址 |

Token Account 与 ATA 的关系：

- Token Account 是 SPL Token 真正记录余额的账户，表达的是“某个 owner 持有某个 mint 的余额”。一个 Token Account 只能对应一个 mint。
- ATA 是 Associated Token Account，是按 `wallet owner + token mint + token program id` 确定性派生出来的标准 Token Account。
- ATA 一定是 Token Account，但 Token Account 不一定是 ATA；协议金库、托管账户、做市账户或历史系统都可能使用普通非 ATA Token Account。
- 用户主地址本身不是 SPL Token 余额账户。给用户转 USDC/USDT 时，真实目标通常是该用户地址对应 mint 的 ATA，而不是用户主地址本身。
- 如果目标 ATA 不存在，提现交易可以附加创建 ATA 指令，但 rent-exempt 成本由谁承担、失败后是否重试，必须在业务策略中明确。

交易所使用建议：

- 用户独立充值地址模式下，应为 `user wallet + mint + token program id` 派生并记录 ATA，充值入账以命中的 token account、owner、mint 和余额增量为准。
- 提现到外部钱包地址时，优先推导目标 owner 的 ATA；如果用户直接填写 Token Account 地址，必须链上校验该账户的 owner、mint、token program id 是否与提现请求一致。
- 扫链时不要只识别 ATA，也要能识别归属我方的普通 Token Account，避免协议金库、归集账户或异常入金漏记。
- 数据模型建议同时保存 `wallet_address`、`mint_address`、`token_program_id`、`ata_address/token_account_address` 和归属用户，不能只保存一个 `to` 地址。

与 EVM 的核心差异：

- SOL 主币地址本身有 lamports 余额。
- SPL Token 余额不在用户主地址上，而在 Token Account/ATA 中。
- 一笔交易可以包含多个 instruction，多个账户读写，不能只按 `from/to/value` 简化。
- `preBalances/postBalances` 和 `preTokenBalances/postTokenBalances` 是交易解析的重要依据。

## 4. 地址体系

Solana 地址是 ed25519 公钥或 program-derived address 的 base58 表示。普通钱包地址通常是 32 字节公钥编码结果。

工程建议：

- 地址字段建议 `VARCHAR(64)` 起步；若统一多链地址表，建议 `VARCHAR(120)`。
- 不要假设地址有固定前缀，主网/devnet 地址本身不带网络前缀。
- 验址必须做 base58 解码并确认长度为 32 字节；还要确认是否允许 PDA 作为目标地址。
- 派生路径建议遵循 SLIP-0044 coin type 501，常见路径：

```text
m/44'/501'/account'/change'
m/44'/501'/account'/0'
```

不同钱包实现路径存在差异，生产上线前必须固定路径规范并写入 `keyman_derived_addresses.derivation_path`，避免迁移时地址不可恢复。

## 5. 密码学算法

Solana 普通账户使用 ed25519 签名。

对 keyman 的影响：

- 当前项目主要演示 BIP32 secp256k1；Solana 需要新增 ed25519 派生与签名能力。
- 推荐使用 SLIP-0010 ed25519 硬化派生，不要把 secp256k1 BIP32 逻辑直接套用到 Solana。
- 公钥为 32 字节，签名为 64 字节 ed25519 signature。
- 远程签名服务不应只接收“任意 32 字节 hash 并签名”。Solana 签名的是序列化 message bytes，签名前必须由 wallet 服务和 keyman 双方约束交易内容。

签名审计建议记录：

- `chain=SOL`
- signer pubkey/address
- recent blockhash 或 durable nonce
- message hash/摘要
- 业务类型：withdraw/sweep/create_ata/token_transfer
- 目标地址、mint、amount、fee payer、instruction 摘要
- 签名结果和拒绝原因

## 6. 交易构建与签名

Solana 交易由 signatures 和 message 组成。message 包含：

- header
- recent blockhash
- account keys
- instructions
- address lookup table 信息（v0 transaction 可选）

### 6.1 防重放机制

普通交易使用 recent blockhash。blockhash 有有效期，过期后交易无法上链。RPC 通常通过 `getLatestBlockhash` 返回 `blockhash` 和 `lastValidBlockHeight`。

工程影响：

- 构建交易时必须保存 `recent_blockhash` 和 `last_valid_block_height`。
- 交易超时后不能简单重复广播旧 rawtx，需要重新构建并重签。
- 不存在 EVM nonce 卡住问题，但存在 blockhash 过期、RPC 未确认、重复广播、交易落地状态不明的问题。
- 大额离线签名或长时间审批可考虑 durable nonce，但会增加 Nonce Account 管理复杂度。

### 6.2 SOL 转账 instruction

SOL 主币提现通常使用 System Program transfer：

```text
from_pubkey
to_pubkey
lamports
```

签名前必须校验：

- fee payer 是系统热钱包或指定付款账户。
- from 是系统控制地址。
- to 等于业务提现目标地址。
- lamports 等于提现单金额。
- fee 上限合理。
- recent blockhash 未过期。
- 交易中没有额外未知 instruction。

### 6.3 SPL Token 转账 instruction

SPL Token 转账不是从用户主地址直接转，而是从 source token account 到 destination token account。通常应使用 `TransferChecked`，因为它带 mint 和 decimals 校验。

签名前必须校验：

- mint 在白名单中。
- source token account 归属系统 owner，且 mint 匹配。
- destination token account 的 owner 是提现目标用户，mint 匹配。
- amount 是 token 最小单位整数。
- decimals 与配置/链上 mint 一致。
- 如需创建 ATA，创建目标 ATA 的 rent 由谁承担必须明确。
- transaction 里没有非预期 program instruction。

### 6.4 签后反解析

签名完成后，广播前必须反解析 raw transaction：

- 验证所有签名。
- 核对 account keys、program id、instruction data。
- 核对 SOL lamports 或 Token amount。
- 核对 mint、source ATA、destination ATA、owner。
- 核对 fee payer 和 recent blockhash。
- 将 rawtx、signature、blockhash、last_valid_block_height 落库。

### 6.5 生产案例：SOL 主币提现构建与离线签名

下面示例以“热钱包地址向外部用户地址提现 SOL”为准。Solana 普通交易没有 EVM Nonce，也不是 UTXO 选币模型；生产并发控制重点是业务幂等、热钱包额度、recent blockhash 有效期、同一业务单只能存在一个有效待确认 rawtx，以及 blockhash 过期后的重构重签。

业务请求：

```json
{
  "chain": "SOL",
  "network": "mainnet-beta",
  "business_type": "withdraw",
  "business_id": 910001,
  "from_address": "HotWalletPubkey111111111111111111111111111111",
  "to_address": "ExternalUserPubkey2222222222222222222222222222",
  "amount_lamports": "1000000000",
  "fee_policy": {
    "max_base_fee_lamports": "10000",
    "max_priority_fee_lamports": "200000",
    "allow_compute_budget": true
  }
}
```

链上参数由钱包服务从可信 RPC 获取，不允许由业务请求方传入：

```json
{
  "latest_blockhash": {
    "blockhash": "9sN6...RecentBlockhash",
    "last_valid_block_height": 365000123
  },
  "fee_payer_balance_lamports": "50000000000",
  "from_balance_lamports": "50000000000",
  "rent_exempt_check": "system_account_no_extra_rent"
}
```

unsigned transaction message 使用 legacy 或 v0 均可；首期建议 legacy，等需要 Address Lookup Table 时再支持 v0。SOL 主币提现的最小 message 结构：

```json
{
  "version": "legacy",
  "fee_payer": "HotWalletPubkey111111111111111111111111111111",
  "recent_blockhash": "9sN6...RecentBlockhash",
  "last_valid_block_height": 365000123,
  "account_keys": [
    {
      "pubkey": "HotWalletPubkey111111111111111111111111111111",
      "signer": true,
      "writable": true,
      "role": "fee_payer_and_source"
    },
    {
      "pubkey": "ExternalUserPubkey2222222222222222222222222222",
      "signer": false,
      "writable": true,
      "role": "withdraw_target"
    },
    {
      "pubkey": "11111111111111111111111111111111",
      "signer": false,
      "writable": false,
      "role": "system_program"
    }
  ],
  "instructions": [
    {
      "program_id": "11111111111111111111111111111111",
      "program": "system",
      "type": "transfer",
      "accounts": {
        "from": "HotWalletPubkey111111111111111111111111111111",
        "to": "ExternalUserPubkey2222222222222222222222222222"
      },
      "lamports": "1000000000"
    }
  ]
}
```

如果需要 priority fee，可在 transfer 之前加入 Compute Budget instruction：

```json
[
  {
    "program": "compute_budget",
    "type": "setComputeUnitLimit",
    "units": 200000
  },
  {
    "program": "compute_budget",
    "type": "setComputeUnitPrice",
    "micro_lamports": 1000
  },
  {
    "program": "system",
    "type": "transfer",
    "lamports": "1000000000"
  }
]
```

费用估算：

```text
signer_count      = 1
base_fee          = 5000 lamports
priority_fee      = compute_unit_limit * compute_unit_price / 1_000_000
                 = 200000 * 1000 / 1_000_000 = 200 lamports
estimated_fee     = 5200 lamports
total_debit       = 1000000000 + 5200 lamports
```

签名前策略校验：

- `from_address` 和 `fee_payer` 必须是系统热钱包或授权付款地址，且 key id/派生路径已登记。
- `to_address` 必须通过 base58 和 32 字节长度校验；若禁止提现到 PDA，需要额外通过链上 owner/data 策略识别。
- `lamports` 必须等于提现单金额，使用整数，禁止 UI 小数参与计算。
- message 中只允许预期的 System Program transfer 和可选 Compute Budget instruction。
- `recent_blockhash` 必须来自当前主网 RPC，`last_valid_block_height` 必须未过期。
- 估算手续费不能超过业务和风控阈值；如果启用 priority fee，必须单独校验 compute unit limit 和 unit price。
- 同一 `business_id` 在 DB 中只能有一个 `Built/Signed/Broadcast` 中的有效交易；blockhash 过期前不能并发重签多笔不同 rawtx。

签名服务请求必须传序列化 message bytes 和可读策略上下文，不能只传调用方计算好的 32 字节摘要：

```json
{
  "request_id": "sol-sign-910001-1",
  "chain": "SOL",
  "network": "mainnet-beta",
  "business_type": "withdraw",
  "business_id": 910001,
  "message_encoding": "base64",
  "message_bytes": "<base64 serialized solana message>",
  "message_summary": {
    "fee_payer": "HotWalletPubkey111111111111111111111111111111",
    "recent_blockhash": "9sN6...RecentBlockhash",
    "last_valid_block_height": 365000123,
    "instructions": [
      {
        "program_id": "11111111111111111111111111111111",
        "type": "transfer",
        "from": "HotWalletPubkey111111111111111111111111111111",
        "to": "ExternalUserPubkey2222222222222222222222222222",
        "lamports": "1000000000"
      }
    ]
  },
  "signers": [
    {
      "pubkey": "HotWalletPubkey111111111111111111111111111111",
      "key_id": "sol-hot-001",
      "derivation_path": "m/44'/501'/0'/0'"
    }
  ],
  "policy": {
    "max_fee_lamports": "210000",
    "allowed_program_ids": [
      "11111111111111111111111111111111",
      "ComputeBudget111111111111111111111111111111"
    ],
    "require_no_extra_instructions": true
  }
}
```

签名机/HSM 处理逻辑：

1. 反序列化 `message_bytes`，确认 header、account keys、recent blockhash、instruction 与 `message_summary` 完全一致。
2. 校验 signer pubkey 与 key id/派生路径派生出的 ed25519 公钥一致。
3. 校验 program id 白名单：SOL 主币提现只允许 System Program transfer 和可选 Compute Budget。
4. 校验 transfer 的 from、to、lamports、fee payer、recent blockhash、last valid height 符合策略。
5. 对 Solana message bytes 直接做 ed25519 签名，返回 64 字节 signature。

签名响应：

```json
{
  "request_id": "sol-sign-910001-1",
  "approved": true,
  "signatures": [
    {
      "pubkey": "HotWalletPubkey111111111111111111111111111111",
      "signature": "<base58 64-byte-ed25519-signature>"
    }
  ],
  "signer_audit_id": "sol-hot-hsm-20260604-000001"
}
```

在线钱包服务组装 signed transaction 后，广播前必须执行：

- 反序列化 raw transaction，确认 `signatures[0]` 对应 fee payer，且签名能用 fee payer pubkey 验证。
- 重新解析 message，确认没有新增 instruction，没有替换 program id，没有替换目标地址或金额。
- 调用 `getFeeForMessage` 或本地费率逻辑复算费用，并确认不超过 `max_fee_lamports`。
- 调用 `simulateTransaction` 做预执行；失败时不广播，记录失败原因并保持业务单可重构。
- 写入 `wallet_sol_txs`：`business_id`、`rawtx`、`signature`、`recent_blockhash`、`last_valid_block_height`、`fee_payer`、`fee`、`status=Signed`。
- 调用 `sendTransaction` 广播，成功返回 signature 只表示 RPC 接受，不表示已确认；随后用 `getSignatureStatuses` 轮询到 `confirmed/finalized`。

广播与状态机：

```text
Created -> Built -> Signed -> Broadcast -> Confirmed -> Finalized
Built/Signed -> Expired        当前 block height > last_valid_block_height 且链上无状态
Broadcast -> Finalized         getSignatureStatuses 返回 finalized 且 err=null
Broadcast -> Failed            getSignatureStatuses 返回 err != null
Broadcast -> Unknown           RPC 超时/节点不一致，进入多节点补偿查询
Expired/Failed -> Rebuilt      重新获取 blockhash、重构 message、重新签名
```

关键处理：

- blockhash 过期后不能继续重播旧 rawtx，必须重新获取 blockhash 并重签。
- 如果 `sendTransaction` 超时但后续查到 signature 已上链，不得重复扣账或重签。
- 如果多节点状态不一致，以 finalized 查询和自建扫块结果为准；不确定时暂停该业务单自动重试。
- 失败交易 `meta.err != null` 不能记为提现成功，必须进入失败/人工处理状态。

本目录已按 `08-btc-manager` 的格式拆成两个可运行 demo：

#### 6.5.1 交易签名发送：`09-sol-manager/tx-sign-send/`

- `slip10.go`：SLIP-0010 ed25519 硬化派生，演示 `m/44'/501'/account'/change'`。
- `base58.go`：Solana 公钥地址 base58 编码/解码和 32 字节验址。
- `transaction.go`：legacy message 编码、System Program transfer instruction、签名前策略校验、ed25519 离线签名、signed transaction 组装、签后反解析验签。
- `main.go`：跑通“构建 message -> 签名机反解析并签名 -> 签后反解析 -> 输出 rawtx base64”的 SOL 主币提现流程。
- `transaction_test.go`：覆盖 SOL transfer 构建签名、签后校验和错误金额拒绝。

运行：

```bash
go run ./09-sol-manager/tx-sign-send
```

该 demo 输出：

- 热钱包地址和派生路径。
- 外部用户提现目标地址。
- recent blockhash/last valid block height。
- 序列化 message base64。
- 签名机返回的 ed25519 signature。
- 签后反解析得到的 fee payer、from、to、lamports、fee、message hash 和 rawtx base64。

注意：demo 为了不引入外部 SDK，手写了 Solana legacy message 最小编码，目的是把生产边界讲清楚。生产实现建议使用成熟 Solana SDK 覆盖更多交易版本、Address Lookup Table、Compute Budget、SPL Token、ATA 和 durable nonce，但仍必须保留 demo 中体现的签名前/签后反解析校验。

#### 6.5.2 区块交易解析：`09-sol-manager/block-tx-parser/`

- `parser.go`：解析 Solana RPC `getBlock/getTransaction` 的 `jsonParsed` 结果，提取 signature、fee payer、err、SOL transfer、pre/post balance delta、SPL token balance delta。
- `fetch.go`：Solana JSON-RPC `getBlock`、`getTransaction` 拉取示例。
- `sample.go`：离线 mock finalized block，用于演示 SOL 主币转账解析。
- `main.go`：提供 `sample`、`parse-block`、`parse-transaction` 三个入口。

运行离线解析：

```bash
CGO_ENABLED=0 go run ./09-sol-manager/block-tx-parser -mode sample
```

解析真实 RPC 区块：

```bash
CGO_ENABLED=0 go run ./09-sol-manager/block-tx-parser \
  -mode parse-block \
  -rpc-url https://api.mainnet-beta.solana.com \
  -slot <slot>
```

解析本地保存的 `getBlock` JSON：

```bash
CGO_ENABLED=0 go run ./09-sol-manager/block-tx-parser \
  -mode parse-block \
  -input-file /tmp/sol-block.json
```

生产扫块不能只看 parsed instruction：

- SOL 主币入账要结合 `preBalances/postBalances`，过滤 `meta.err != null` 的失败交易。
- 手续费会体现在 fee payer 的余额 delta 中，不能误当用户转出金额。
- SPL Token 入账优先解析 `preTokenBalances/postTokenBalances`，以 mint、token account、owner、decimals 为准。
- `getBlock`/`getTransaction` 的公共 RPC 历史能力可能受限，生产应使用自建 RPC、归档能力或可靠索引器做补偿。

### 6.6 生产案例扩展：SPL Token 提现 TransferChecked

SPL Token 提现使用 Token Program 或 Token-2022 Program 的 `TransferChecked`。不要用 symbol 判断资产，必须以 mint address 和 token program id 为准。

业务请求：

```json
{
  "chain": "SOL",
  "business_type": "token_withdraw",
  "business_id": 910002,
  "mint": "USDCMintPubkey333333333333333333333333333333",
  "token_program": "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
  "source_token_account": "HotUSDCATA4444444444444444444444444444444",
  "source_owner": "HotWalletPubkey111111111111111111111111111111",
  "destination_owner": "ExternalUserPubkey2222222222222222222222222222",
  "destination_token_account": "ExternalUserUSDCATA555555555555555555555555555",
  "amount_base_units": "25000000",
  "decimals": 6
}
```

签名前必须补充链上校验：

- `mint` 命中 Token 白名单，`decimals` 与链上 mint 和配置一致。
- `source_token_account` 的 owner 是系统热钱包，mint 匹配，余额足够，未冻结，无异常 delegate。
- `destination_token_account` 是 `destination_owner + mint + token_program` 推导出的 ATA，或已通过链上账户校验确认 owner/mint 正确。
- 若目标 ATA 不存在，交易可附加 `createAssociatedTokenAccount`，但必须把 rent-exempt 成本、payer 和失败处理写入业务策略。
- Token-2022 mint 必须额外检查 Transfer Fee、Transfer Hook、Memo Transfer、Default Account State、Permanent Delegate 等扩展，不符合白名单策略则拒签。

unsigned instruction 摘要：

```json
{
  "instructions": [
    {
      "program": "spl_token",
      "program_id": "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
      "type": "transferChecked",
      "accounts": {
        "source": "HotUSDCATA4444444444444444444444444444444",
        "mint": "USDCMintPubkey333333333333333333333333333333",
        "destination": "ExternalUserUSDCATA555555555555555555555555555",
        "authority": "HotWalletPubkey111111111111111111111111111111"
      },
      "amount": "25000000",
      "decimals": 6
    }
  ]
}
```

签后和广播前校验除 SOL 主币规则外，还必须反解析 `TransferChecked` instruction，确认：

- program id 是白名单中的 Token Program 或已批准的 Token-2022 Program。
- source、destination、mint、authority、amount、decimals 与提现单一致。
- 没有 approve、setAuthority、closeAccount、freeze/thaw 等非预期 instruction。
- 如果包含 ATA 创建，创建目标只能是提现目标 owner 的 ATA，payer 和 rent 成本与业务策略一致。

落库建议：

```text
wallet_sol_txs:
  signature, rawtx, recent_blockhash, last_valid_block_height, fee_payer, fee, status

wallet_sol_instructions:
  signature, instruction_index, program_id, parsed_type=transferChecked,
  source, destination, owner/authority, mint, amount, decimals

wallet_outbound / token_outbound_ext:
  business_id, mint, token_program, source_token_account, destination_token_account,
  amount_base_units, decimals, status, signature
```

## 7. Token、NFT、Memo

### 7.1 SPL Token

Solana Token 体系由 Token Program 管理。Token 的唯一标识是 mint address，不是 symbol。

充值解析建议：

- Token 白名单以 mint address 为准。
- 对每个支持的 mint 记录 decimals、token program id、是否 Token-2022、是否有特殊扩展。
- 优先解析交易 meta 中的 `preTokenBalances/postTokenBalances`。
- 入账金额以目标 token account 的余额增量为准，而不是只信 instruction data。
- 充值唯一键建议包含 `chain + signature + instruction_index/inner_index + mint + token_account`。

### 7.2 Associated Token Account

ATA 由 wallet owner + token mint + token program 派生，能减少“这个 token account 属于谁”的歧义。

交易所可选模型：

1. 每个用户每个 Token 分配独立 ATA。
   - 优点：归属清晰，memo 不是必需。
   - 缺点：创建 ATA 需要 rent-exempt 余额，批量支持 Token 成本更高。
2. 使用共享 Token Account + Memo。
   - 优点：地址/账户数量少。
   - 缺点：漏填 memo 会导致自动入账失败，风控和客服成本高。

工程建议：首期 SOL 和重点 SPL Token 尽量使用用户独立地址/ATA；只有在业务明确要求共享地址时才使用 memo，并建立漏填处理流程。

### 7.3 Token-2022

Token-2022 支持 Transfer Fee、Transfer Hook、Permanent Delegate、Default Account State、Memo Transfer 等扩展。

生产风险：

- Transfer Fee 会导致发送金额和到账金额不同。
- Transfer Hook 可能引入额外程序调用和失败条件。
- Default Account State 可能导致账户冻结/不可用。
- Memo Transfer 可能强制要求 memo。

首期建议：只支持标准 SPL Token；Token-2022 逐个 mint 评估并通过 FeatureGate 标记。

### 7.4 NFT

Solana NFT 常见于 Metaplex 标准，技术上也是 Token/Token-2022 体系的一种扩展。交易所普通 SOL/SPL Token 钱包首期不建议支持 NFT。

若不支持 NFT：

- 不展示 NFT 资产。
- 不自动归集未知 mint。
- 不误花包含 NFT 或未知 mint 的 token account。
- 客服文档说明误转 NFT 不自动入账。

### 7.5 Memo

Solana Memo Program 可以在交易中附加文本 memo。SOL 主币转账本身不强制 memo。

交易所策略：

- 独立地址模式：memo 可选，不作为入账必要条件。
- 共享地址模式：memo 必须纳入用户归属，充值缺 memo 进入人工处理。
- 某些 Token-2022 mint 可能通过扩展强制 memo，必须逐 mint 调研。

## 8. RPC 调研

### 8.1 必查 RPC

| 场景 | RPC/接口 | 官方支持 | 是否需额外索引 | 生产建议 |
|------|----------|----------|----------------|----------|
| 同步状态 | `getHealth`、`getSlot`、`getBlockHeight`、`getEpochInfo` | 支持 | 否 | 多节点比对 slot/block height |
| 最新 blockhash | `getLatestBlockhash` | 支持 | 否 | 构建交易必须保存 `lastValidBlockHeight` |
| 获取区块 | `getBlock` | 支持 | 历史数据依赖节点保留/BigTable | 扫块用 finalized commitment，开启 transaction details |
| 获取交易 | `getTransaction` | 支持 | 长期历史依赖节点/供应商能力 | 二次校验提现和充值 |
| 地址签名历史 | `getSignaturesForAddress` | 支持 | 深历史可能受限 | 可用于补偿扫描，但不应替代区块同步 |
| SOL 余额 | `getBalance` | 支持 | 否 | 对账使用 finalized |
| Token 余额 | `getTokenAccountBalance` | 支持 | 否 | Token account 对账 |
| owner 下 Token 账户 | `getTokenAccountsByOwner` | 支持 | 否/大量账户需分页策略 | 用户/热钱包 Token 对账 |
| 广播交易 | `sendTransaction` | 支持 | 否 | 设置 preflight 和 max retries 策略 |
| 查询签名状态 | `getSignatureStatuses` | 支持 | 历史需 `searchTransactionHistory` | 提现状态机核心 |
| 费用 | `getFeeForMessage`、`getRecentPrioritizationFees` | 支持 | 否 | 估算 base fee 和 priority fee |
| 最低租金 | `getMinimumBalanceForRentExemption` | 支持 | 否 | 创建 ATA/Nonce Account 前计算成本 |

### 8.2 扫块 RPC 使用顺序

生产扫块建议以 finalized slot 为入账边界，按批次推进。`getBlocks` 用来发现一段 slot 中实际产出的 block，`getBlock` 用来拉取并解析具体 block。skipped slot 本身没有交易，不能因为 skipped 漏扫；真正的风险是把 RPC 异常、节点未同步或历史裁剪误判成 skipped。

核心 RPC 和用途：

| 顺序 | RPC | 主要用途 | 使用要点 |
|------|-----|----------|----------|
| 1 | `getHealth` | 判断 RPC 节点是否健康 | 不健康节点不参与扫块和入账 |
| 2 | `getSlot` | 获取当前 slot 边界 | 使用 `commitment=finalized` 作为入账扫描上限 |
| 3 | `getBlockHeight` | 获取当前 block height | 用于交易 blockhash 过期判断，不等同于 slot |
| 4 | `getFirstAvailableBlock` / `minimumLedgerSlot` | 判断节点最早可查历史 | 历史重扫前确认起始 slot 没有超出节点保留范围 |
| 5 | `getBlocks` / `getBlocksWithLimit` | 获取范围内实际有 block 的 slot 列表 | 返回列表不包含 skipped slot；适合批量发现可解析 block |
| 6 | `getBlock` | 拉取 block 详情和交易列表 | 使用 finalized、`transactionDetails=full`，并设置 `maxSupportedTransactionVersion` |
| 7 | `getTransaction` | 单笔交易二次校验或补偿查询 | 用于充值复核、提现状态补偿、解析异常交易复查 |
| 8 | `getSignatureStatuses` | 查询提现交易状态 | 广播后轮询状态；深历史需要 `searchTransactionHistory=true` |

推荐调用流程：

1. 启动时对多个 RPC 节点调用 `getHealth`、`getSlot(finalized)`，剔除明显落后的节点。
2. 读取本地 checkpoint，得到 `next_slot = last_scanned_slot + 1`。
3. 调 `getSlot` 获取 `finalized_slot`，本轮只扫描 `next_slot <= slot <= finalized_slot` 的范围。
4. 历史重扫或节点切换时，先调 `getFirstAvailableBlock` 或 `minimumLedgerSlot`，确认 `next_slot` 没有早于节点可查历史。
5. 按小批次调用 `getBlocks(start_slot, end_slot, finalized)`，得到这段范围内实际产出 block 的 slot 列表。
6. 对 `getBlocks` 返回的每个 slot 调 `getBlock(slot, finalized)`，解析交易、instruction、`preBalances/postBalances`、`preTokenBalances/postTokenBalances`。
7. 对没有出现在 `getBlocks` 返回值里的 slot，只有在 RPC 正常、范围 finalized、批次查询成功时，才记录为 `Skipped`。
8. 某笔交易解析失败、字段缺失或入账前需要复核时，用 `getTransaction(signature, finalized)` 做二次校验。
9. 提现广播后的状态机用 `getSignatureStatuses` 轮询，必要时再用 `getTransaction` 拉完整 meta 校验 `err`、fee 和实际指令。
10. 一个批次内所有 slot 都进入 `Parsed` 或 `Skipped` 后，才推进 checkpoint；遇到 RPC 错误、超时、`Block not available`、节点落后时不能推进。

典型请求参数：

```json
{
  "method": "getSlot",
  "params": [
    {
      "commitment": "finalized"
    }
  ]
}
```

```json
{
  "method": "getBlocks",
  "params": [
    300000000,
    300000100,
    {
      "commitment": "finalized"
    }
  ]
}
```

```json
{
  "method": "getBlock",
  "params": [
    300000010,
    {
      "commitment": "finalized",
      "encoding": "jsonParsed",
      "transactionDetails": "full",
      "rewards": false,
      "maxSupportedTransactionVersion": 0
    }
  ]
}
```

`getBlock` 解析重点：

- `blockhash`、`parentSlot`、`previousBlockhash`：用于本地 block 索引和节点一致性校验。
- `blockTime`：作为链上时间，可能为空，不能作为唯一排序依据。
- `transactions[].transaction.signatures[0]`：Solana 交易 ID。
- `transactions[].meta.err`：非空表示交易执行失败，不能入账。
- `transactions[].meta.fee`：实际手续费，通常由 fee payer 扣除。
- `preBalances/postBalances`：SOL 余额变化，解析主币充值和手续费影响。
- `preTokenBalances/postTokenBalances`：SPL Token 余额变化，解析 Token 充值时优先使用。
- `message.instructions` 和 `meta.innerInstructions`：解析 System、Token、ATA、Memo 等具体业务语义。

异常处理规则：

- `getBlocks` 成功返回但某些 slot 缺失：这些 slot 可记录为 `Skipped`。
- `getBlocks` 超时、限流、节点错误：整个批次进入重试，不能把缺失 slot 当 skipped。
- `getBlock` 返回 skipped/空块类结果：与 `getBlocks` 和备用节点交叉确认后记录 `Skipped`。
- `getBlock` 返回 `Block not available`、历史不可用或版本不支持：切换节点或调整参数，不能推进 checkpoint。
- 单节点结果与备用节点不一致：暂停该范围自动入账，进入多节点复查或人工处理。

### 8.3 区块同步策略

Solana 可以按 slot 拉 `getBlock`。注意 slot 不等于连续产出块，可能有 skipped slot。

同步器建议：

- 维护本地 `slot -> blockhash,parentSlot,previousBlockhash,blockTime`。
- 使用 finalized commitment 扫描。
- 优先用 `getBlocks` 批量发现实际产出 block 的 slot，再对这些 slot 调 `getBlock`。
- 对 skipped slot 记录跳过或只推进高度指针，不应当成异常块。
- checkpoint 只有在 block 已解析或 slot 已确认 skipped 后才能推进。
- 对 RPC 返回 `Block not available`、历史裁剪、节点落后做重试和节点切换。
- 对每个交易解析 `meta.err`，失败交易不能入账。
- Token 充值优先基于 `preTokenBalances/postTokenBalances` 计算余额变化。

### 8.4 地址交易历史

`getSignaturesForAddress` 可以按地址查签名，但生产记账不能只依赖它：

- Token 充值命中的是 token account，不一定是 owner 主地址。
- 一个交易可能包含多个 instruction 和 inner instruction。
- 深历史查询取决于节点是否保留足够数据。
- RPC 供应商可能限流或裁剪。

工程建议：以扫 finalized block 为主，地址签名历史用于补偿、排障和初始化校验。

## 9. 节点部署调研

官方生态常用 Solana/Agave 验证者和 RPC 节点。交易所钱包建议自建或采购高可靠 RPC，但不要只依赖单一公共 RPC。

生产节点建议：

- 至少两个独立 RPC 来源，最好不同机器/机房/供应商。
- RPC 只对内网服务开放，禁止公网裸露。
- 独立监控 slot 落后、root 落后、健康状态、错误率、延迟。
- 如果需要长期历史交易，评估 BigTable、Geyser 插件、Yellowstone gRPC、第三方索引器或自建索引。
- 区块扫描服务应支持节点切换和重复扫描幂等。

历史重扫注意：

- 普通 RPC 节点不一定保存完整历史。
- `getBlock/getTransaction/getSignaturesForAddress` 对旧数据的可用性依赖节点配置。
- 交易所上线前必须确定业务起始 slot，并保存自建索引结果。

## 10. 费用模型

官方资料确认：

- 交易 base fee 按签名数收取，常见为每个签名 5000 lamports。
- 交易可以通过 Compute Budget instruction 设置 compute unit limit 和 compute unit price，形成 priority fee。
- 创建账户需要达到 rent-exempt 最低余额，Token Account/ATA 创建会占用 SOL。

费用组成：

```text
总成本 = base fee + priority fee + 可选账户创建 rent-exempt 成本
priority fee = compute_unit_limit * compute_unit_price
```

示例：

```text
1 笔普通 SOL 转账：
签名数 = 1
base fee ≈ 5000 lamports
priority fee = 0 时，总手续费约 5000 lamports
```

SPL Token 提现可能额外包含：

- 创建目标 ATA 的 rent-exempt lamports。
- Token transfer instruction 手续费。
- priority fee。

工程建议：

- 提现表中区分 `network_fee` 和 `account_creation_cost`。
- 如果交易所为用户创建目标 ATA，需要业务明确该成本由谁承担。
- 手续费上限不能只看 base fee，必须把 priority fee 和创建账户成本一起纳入风控。

## 11. 重组、finality 和历史事故

Solana 使用 commitment 暴露不同确认级别：

- `processed`：当前节点已处理，风险最高。
- `confirmed`：集群多数投票确认，风险较低。
- `finalized`：达到最大锁定确认，通常作为生产最终状态。

工程建议：

- 充值入账使用 `finalized`。
- 提现广播后可先标记 `Broadcast/Pending`，`confirmed/finalized` 后再推进状态。
- 本地仍需保存 slot/blockhash，用于处理 RPC 不一致、回滚或节点切换。
- 发现 finalized slot 回退、节点返回冲突或长时间不可用时，应暂停入账和出账。

历史风险：

- Solana 主网历史上发生过多次停机或性能事故，包括 2024-02-06 Mainnet Beta outage。
- 对交易所钱包而言，停链期间应暂停提现广播或标记为待广播，避免状态误判。
- RPC 延迟或分叉期间，不能以单节点 `processed` 结果给用户上账。

确认数建议：

- demo：`confirmed` 可用于演示。
- 普通生产：`finalized` 入账。
- 大额：`finalized` 后增加风控延迟或人工复核。

## 12. Memo/Tag 策略

SOL 主币不强制 memo。交易所首期建议：

- 每个用户派生独立 SOL 地址，不要求 memo。
- SPL Token 优先为用户地址派生/创建对应 ATA，不要求 memo。
- 若业务采用共享地址，必须强制 memo，并把 `memo` 纳入充值归属和唯一键。

漏填 memo 处理：

- 充值进入待人工认领表。
- 用户提供签名、金额、时间、来源交易所证明等材料。
- 人工核对后补账，流程必须有审计记录。

## 13. 浏览器和开发资料

官方/权威资料：

- Solana 官方文档：https://solana.com/docs
- Solana JSON-RPC API：https://solana.com/docs/rpc
- Solana 交易与费用文档：https://solana.com/docs/core/transactions
- Solana 账户模型：https://solana.com/docs/core/accounts
- Solana Token 文档：https://solana.com/docs/tokens
- SPL Token Program：https://spl.solana.com/token
- Associated Token Account Program：https://spl.solana.com/associated-token-account
- Memo Program：https://spl.solana.com/memo
- Anza validator 文档：https://docs.anza.xyz/
- Solana Status：https://status.solana.com/

浏览器/数据平台：

- Solana Explorer：https://explorer.solana.com/
- Solscan：https://solscan.io/
- SolanaFM：https://solana.fm/
- CoinMarketCap SOL：https://coinmarketcap.com/currencies/solana/

## 14. 数据库设计建议

现有表存在 EVM 假设：

- `address VARCHAR(42)` 不适合 Solana。
- `hash VARCHAR(66)` 不适合 Solana signature，Solana signature base58 长度通常约 88 字符。
- `nonce` 对普通 Solana 交易无意义。
- Token 充值不能只靠 `to` 地址，需要 token account、owner、mint、instruction index。

### 14.1 地址表调整

建议多链地址字段统一放宽：

```sql
address VARCHAR(120) NOT NULL COMMENT '链上地址字符串；Solana 为 base58 公钥或 PDA'
pubkey VARBINARY(65) NULL COMMENT '原始公钥字节；Solana 普通钱包公钥为 32 字节，统一多链可放宽到 65 字节'
derivation_path VARCHAR(120) NOT NULL DEFAULT '' COMMENT 'HD 派生路径；Solana 建议 SLIP-0010 ed25519 硬化路径'
```

Solana 公钥 32 字节，地址为 base58。

### 14.2 Solana slot 扫描状态表

`wallet_sol_slots` 记录每个已处理或异常的 slot。未出现记录表示尚未扫描，不需要提前插入 `Pending`。

```sql
CREATE TABLE `wallet_sol_slots` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '自增主键',
    `chain` VARCHAR(20) NOT NULL DEFAULT 'SOL' COMMENT '链标识',
    `slot` BIGINT NOT NULL COMMENT 'Solana slot，扫描推进主键',
    `status` TINYINT NOT NULL COMMENT '1=Parsed 2=Skipped 3=Retry 4=Failed',
    `scan_attempts` INT NOT NULL DEFAULT 0 COMMENT '扫描尝试次数',
    `last_error` VARCHAR(512) NOT NULL DEFAULT '' COMMENT '最近一次扫描错误，正常为空',
    `ctime` DATETIME NOT NULL COMMENT '记录创建时间',
    `mtime` DATETIME NOT NULL COMMENT '记录最后更新时间',
    UNIQUE KEY `uk_chain_slot` (`chain`, `slot`),
    KEY `idx_chain_status_slot` (`chain`, `status`, `slot`)
) ENGINE=InnoDB COMMENT='Solana slot 扫描状态表';
```

状态含义：

- `Parsed`：该 slot 真实产出 block，且 block、交易和 instruction 已解析入库。
- `Skipped`：该 slot 已确认没有产出 block。
- `Retry`：临时失败，例如 RPC 超时、限流、节点落后，等待重试。
- `Failed`：多次重试仍失败，需要切换节点或人工处理。

### 14.3 Solana block 索引表

`wallet_sol_blocks` 只记录真实产出 block 的 slot。skipped slot 不写入该表。

```sql
CREATE TABLE `wallet_sol_blocks` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '自增主键',
    `chain` VARCHAR(20) NOT NULL DEFAULT 'SOL' COMMENT '链标识',
    `slot` BIGINT NOT NULL COMMENT '该 block 所在 slot',
    `block_hash` VARCHAR(128) NOT NULL COMMENT 'Solana blockhash',
    `parent_slot` BIGINT NOT NULL COMMENT '父 block 所在 slot，不一定等于 slot-1',
    `previous_blockhash` VARCHAR(128) NOT NULL COMMENT '父 block 的 blockhash',
    `block_height` BIGINT NOT NULL DEFAULT 0 COMMENT 'Solana block height；RPC 不返回时记 0',
    `block_time` DATETIME NULL COMMENT '链上 block time，RPC 可能为空',
    `tx_count` INT NOT NULL DEFAULT 0 COMMENT 'block 内交易数量',
    `ctime` DATETIME NOT NULL COMMENT '记录创建时间',
    UNIQUE KEY `uk_chain_slot` (`chain`, `slot`),
    UNIQUE KEY `uk_chain_block_hash` (`chain`, `block_hash`),
    KEY `idx_chain_parent_slot` (`chain`, `parent_slot`)
) ENGINE=InnoDB COMMENT='Solana block 索引表';
```

### 14.4 Solana 交易表

```sql
CREATE TABLE `wallet_sol_txs` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '自增主键',
    `chain` VARCHAR(20) NOT NULL DEFAULT 'SOL' COMMENT '链标识，例如 SOL',
    `signature` VARCHAR(128) NOT NULL COMMENT 'Solana 交易签名，等价交易ID',
    `slot` BIGINT NOT NULL DEFAULT 0 COMMENT '交易所在 slot；未确认或本地构建阶段为 0',
    `block_hash` VARCHAR(128) NOT NULL DEFAULT '' COMMENT '交易最终所在区块 hash；finalized 后补齐',
    `recent_blockhash` VARCHAR(128) NOT NULL DEFAULT '' COMMENT '构建交易时使用的 recent blockhash，用于过期判断和审计',
    `last_valid_block_height` BIGINT NOT NULL DEFAULT 0 COMMENT 'recent blockhash 对应的最后有效 block height，过期后需重构重签',
    `fee_payer` VARCHAR(120) NOT NULL DEFAULT '' COMMENT '支付交易手续费的 Solana 地址，通常是热钱包或指定付款账户',
    `fee` VARCHAR(40) NOT NULL DEFAULT '0' COMMENT '链上实际手续费，单位 lamports，使用字符串避免精度问题',
    `compute_units_consumed` BIGINT NOT NULL DEFAULT 0 COMMENT '交易实际消耗的 compute units；用于费用分析和异常排查',
    `err` TEXT COMMENT '链上执行错误 JSON 或错误文本；成功交易为空',
    `status` TINYINT NOT NULL DEFAULT 0 COMMENT '0=Pending 1=Confirmed 2=Finalized 3=Failed 4=Expired 5=Reverted',
    `rawtx` MEDIUMBLOB COMMENT '已签名 raw transaction 原始字节；用于重播、审计和签后反解析',
    `btime` DATETIME NULL COMMENT '链上区块时间；RPC 无返回时可为空',
    `ctime` DATETIME NOT NULL COMMENT '记录创建时间',
    `mtime` DATETIME NOT NULL COMMENT '记录最后更新时间',
    UNIQUE KEY `uk_signature` (`chain`, `signature`),
    KEY `idx_slot` (`chain`, `slot`),
    KEY `idx_status` (`chain`, `status`)
) ENGINE=InnoDB COMMENT='Solana 交易索引表';
```

### 14.5 Solana instruction 表

```sql
CREATE TABLE `wallet_sol_instructions` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '自增主键',
    `chain` VARCHAR(20) NOT NULL DEFAULT 'SOL' COMMENT '链标识，例如 SOL',
    `signature` VARCHAR(128) NOT NULL COMMENT '所属 Solana 交易签名',
    `slot` BIGINT NOT NULL DEFAULT 0 COMMENT 'instruction 所在交易的 slot',
    `instruction_index` INT UNSIGNED NOT NULL COMMENT '外层 instruction 下标，从 0 开始',
    `inner_index` INT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'inner instruction 下标；外层 instruction 可固定为 0 或按解析约定区分',
    `program_id` VARCHAR(120) NOT NULL COMMENT '执行该 instruction 的 program id，例如 System/Token/Token-2022/Memo Program',
    `parsed_type` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '解析后的指令类型，例如 transfer、transferChecked、createAssociatedTokenAccount、memo',
    `source` VARCHAR(120) NOT NULL DEFAULT '' COMMENT '转出账户；SOL 为付款地址，SPL Token 为 source token account',
    `destination` VARCHAR(120) NOT NULL DEFAULT '' COMMENT '转入账户；SOL 为收款地址，SPL Token 为 destination token account',
    `owner` VARCHAR(120) NOT NULL DEFAULT '' COMMENT 'Token Account 背后的 owner/authority；SOL 主币转账可为空',
    `mint` VARCHAR(120) NOT NULL DEFAULT '' COMMENT 'SPL Token mint address；SOL 主币转账为空',
    `amount` VARCHAR(40) NOT NULL DEFAULT '0' COMMENT '转账金额最小单位；SOL 为 lamports，Token 为 base units',
    `decimals` INT NOT NULL DEFAULT 0 COMMENT 'Token decimals；SOL 主币可为 9 或按主币解析逻辑处理',
    `memo` VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'Memo Program 文本；无 memo 为空',
    `is_ours` TINYINT NOT NULL DEFAULT 0 COMMENT '是否命中我方地址或 token account：0=否 1=是',
    `ctime` DATETIME NOT NULL COMMENT '记录创建时间',
    UNIQUE KEY `uk_ix` (`chain`, `signature`, `instruction_index`, `inner_index`),
    KEY `idx_mint_dest` (`chain`, `mint`, `destination`),
    KEY `idx_slot` (`chain`, `slot`)
) ENGINE=InnoDB COMMENT='Solana 指令解析明细';
```

### 14.6 SPL Token 账户表

```sql
CREATE TABLE `wallet_sol_token_accounts` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '自增主键',
    `chain` VARCHAR(20) NOT NULL DEFAULT 'SOL' COMMENT '链标识，例如 SOL',
    `token_account` VARCHAR(120) NOT NULL COMMENT 'SPL Token Account 地址；可以是 ATA 或普通 Token Account',
    `owner_address` VARCHAR(120) NOT NULL COMMENT '用户主地址或系统地址',
    `uid` BIGINT NOT NULL DEFAULT 0 COMMENT '归属用户 ID；系统热/冷/归集账户可为 0',
    `mint` VARCHAR(120) NOT NULL COMMENT 'Token mint address，资产唯一标识，不能使用 symbol 代替',
    `token_program` VARCHAR(120) NOT NULL COMMENT 'Token Program id；区分 SPL Token 与 Token-2022',
    `is_ata` TINYINT NOT NULL DEFAULT 1 COMMENT '是否为按 owner+mint+token_program 派生的 ATA：1=是 0=普通 Token Account',
    `address_kind` TINYINT NOT NULL DEFAULT 0 COMMENT '账户用途：0=User 1=Hot 2=Cold 3=Sweep 4=Vault 5=Unknown',
    `status` TINYINT NOT NULL DEFAULT 1 COMMENT '1=Active 0=Disabled',
    `ctime` DATETIME NOT NULL COMMENT '记录创建时间',
    `mtime` DATETIME NOT NULL COMMENT '记录最后更新时间',
    UNIQUE KEY `uk_token_account` (`chain`, `token_account`),
    UNIQUE KEY `uk_owner_mint_program` (`chain`, `owner_address`, `mint`, `token_program`),
    KEY `idx_uid` (`uid`, `chain`)
) ENGINE=InnoDB COMMENT='Solana SPL Token Account/ATA 归属表';
```

### 14.7 扫链落库顺序

生产建议按批次调用 `getBlocks(start_slot, end_slot, finalized)`，返回值是实际产出 block 的 slot 列表。只有当本批次 RPC 查询成功、范围已经 finalized、节点健康时，未返回的 slot 才能按 skipped 处理。

推荐处理顺序：

1. 从 checkpoint 读取 `start_slot = last_safe_slot + 1`，再用 `getSlot(finalized)` 得到本轮扫描上限。
2. 调用 `getBlocks(start_slot, end_slot, finalized)`，得到 `block_slots`。
3. 对 `block_slots` 中每个 slot 调 `getBlock(slot, finalized)`。
4. 每个真实 block 成功解析后，先写 `wallet_sol_blocks`。
5. 再写该 block 内的 `wallet_sol_txs` 和 `wallet_sol_instructions`。
6. 真实 block 相关数据全部落库成功后，写 `wallet_sol_slots.status = Parsed`。
7. 对本批次中不在 `block_slots` 的 slot，写 `wallet_sol_slots.status = Skipped`。
8. 本批次所有 slot 都是 `Parsed` 或 `Skipped` 后，推进 checkpoint。

异常处理：

- `getBlocks` 失败：整个批次不判定 skipped，不推进 checkpoint。
- `getBlock` 失败：该 slot 写 `Retry` 或增加重试次数，本批次 checkpoint 不推进。
- `Parsed` / `Skipped` 是终态，后续临时错误不应覆盖为 `Retry`。
- `Retry` / `Failed` 可以被后续成功扫描覆盖为 `Parsed` 或 `Skipped`。
- 多 RPC 返回不一致时，暂停该范围自动入账，进入复查。

差集示例：

```text
start_slot = 100
end_slot   = 110
getBlocks 返回 [100, 103, 104, 108]

Parsed:  100, 103, 104, 108
Skipped: 101, 102, 105, 106, 107, 109, 110
```

### 14.8 Solana Token 充值唯一键

建议 `wallet_inbound` 对 Solana 扩展或新增链特定表：

```text
UNIQUE(chain, signature, instruction_index, inner_index, mint, token_account)
```

SOL 主币充值可用：

```text
UNIQUE(chain, signature, instruction_index, to)
```

不要只用 `hash + to`，因为同一交易内可能多个 instruction 命中同一地址或多个 token account。

## 15. 交易示例：如何转成充值记录

### 15.1 SOL 主币充值

交易包含 System Program transfer：

```json
{
  "signature": "5x...",
  "slot": 300000000,
  "meta": {"err": null, "fee": 5000},
  "instruction": {
    "program": "system",
    "type": "transfer",
    "source": "ExternalUserPubkey",
    "destination": "UserDepositPubkey",
    "lamports": 1000000000
  }
}
```

解析：

1. `meta.err == null`，否则过滤。
2. destination 命中系统用户地址。
3. 插入 `wallet_sol_instructions`。
4. 插入 `wallet_inbound`：

```text
chain=SOL
symbol=SOL
height/slot=300000000
hash/signature=5x...
from=ExternalUserPubkey
to=UserDepositPubkey
value=1000000000
fee=5000
uid=<用户ID>
status=Pending/Success 取决于 finalized 策略
```

### 15.2 SPL Token 充值

交易包含 Token Program `transferChecked`：

```json
{
  "signature": "4a...",
  "slot": 300000010,
  "meta": {
    "err": null,
    "preTokenBalances": [
      {"accountIndex": 3, "mint": "USDCMint", "owner": "UserDepositPubkey", "uiTokenAmount": {"amount": "0", "decimals": 6}}
    ],
    "postTokenBalances": [
      {"accountIndex": 3, "mint": "USDCMint", "owner": "UserDepositPubkey", "uiTokenAmount": {"amount": "25000000", "decimals": 6}}
    ]
  }
}
```

解析：

1. 校验 `meta.err == null`。
2. mint 命中 Token 白名单。
3. token account 命中 `wallet_sol_token_accounts`，owner 对应用户。
4. 用 post-pre 计算到账最小单位：`25000000`。
5. 插入 `wallet_inbound` 或 Solana Token 充值扩展表。

## 16. 对账设计

SOL 主币：

- 本地余额 = 用户 SOL 入账流水 - 提现流水 - 归集流水等。
- 链上余额用 `getBalance(address, finalized)`。
- 热钱包/冷钱包按地址余额对账。

SPL Token：

- 用户 Token 余额按 mint + token account 聚合。
- 链上余额用 `getTokenAccountBalance` 或 `getTokenAccountsByOwner`。
- Token 账户关闭、冻结、delegate、Token-2022 扩展必须纳入异常检查。

对账告警：

- 节点 slot 落后。
- 本地 finalized slot 与 RPC finalized slot 差距过大。
- 本地余额与链上余额不一致。
- 提现 signature 长时间无 finalized 状态。
- blockhash 过期但业务单仍处于待广播/待确认。

## 17. 对本项目的落地建议

### 17.1 coinset 配置

建议在 `internal/coinset` 增加 Solana 特性：

```go
const (
    FeatureSPLToken FeatureGate = 1 << iota // 示例：需接在现有枚举后
    FeatureMemo
    FeatureRecentBlockhash
    FeatureToken2022
)
```

新增链配置：

```go
SOL = Chain{
    Name:         "SOL",
    ChainID:      0,
    Type:         Account,
    Confirms:     1, // 表示 finalized 后入账；实际实现应按 commitment 而不是 EVM 块确认数
    Features:     FeatureSPLToken | FeatureRecentBlockhash | FeatureMemo,
    NativeSymbol: "SOL",
    Decimals:     9,
}
```

注意：当前 `Confirms` 用 `SafeHeight = back - confirms` 表达 EVM 确认数，对 Solana 不完全贴切。更好的方式是在 `Chain` 中增加 finality/commitment 配置，或通过 FeatureGate 让 Solana syncer 使用 `finalized` slot 作为安全高度。

### 17.2 模块拆分

建议围绕 `09-sol-manager/tx-sign-send/` 和 `09-sol-manager/block-tx-parser/` 分阶段实现：

1. 在 `tx-sign-send` 中完善 ed25519 地址派生和 base58 地址校验。
2. 在 `tx-sign-send` 中完善 SOL transfer 交易构建、离线签名、签后反解析。
3. recent blockhash 过期处理和重签流程。
4. 在 `block-tx-parser` 中解析 mock/真实 finalized block，生成 SOL 充值记录。
5. 在 `block-tx-parser` 中完善 SPL Token/ATA 归属表和 Token 充值解析。
6. Memo 模式充值归属和漏填处理。
7. 多节点 finalized slot 对账和提现状态补偿。

### 17.3 最小生产闭环

首期建议支持：

- SOL 主币充值、提现、归集。
- 标准 SPL Token 充值、提现，优先 ATA 模式。
- finalized commitment 入账。
- recent blockhash 过期重签。
- 多节点 RPC 二次校验。
- SOL 和 SPL Token 对账。

首期不建议支持：

- Token-2022 带复杂扩展的 Token。
- NFT/Metaplex 资产。
- durable nonce 大额审批流程。
- 共享地址 + memo 模式，除非业务强需求。
- 依赖单一公共 RPC 作为唯一记账来源。

## 18. 生产安全清单

- ed25519 派生和签名与 EVM secp256k1 完全隔离。
- 地址字段、signature 字段长度已放宽。
- SOL lamports 和 Token amount 均用整数。
- 充值只接受 `meta.err == null` 的 finalized 交易。
- Token 以 mint address 白名单为准，不信 symbol。
- Token amount 用余额变化或 `transferChecked` 校验，不只信 UI amount。
- 提现签名前校验所有 instruction，不允许额外未知 program。
- 签后反解析 raw tx，核对 fee payer、to、mint、amount、recent blockhash。
- blockhash 过期后必须重新构建并重签。
- 多节点 RPC 返回不一致时暂停入账/出账。
- 停链或 slot 长时间不推进时暂停提现广播并告警。

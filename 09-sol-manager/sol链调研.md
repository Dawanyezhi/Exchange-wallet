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

- account keys
- recent blockhash
- instructions
- header
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

### 8.2 区块同步策略

Solana 可以按 slot 拉 `getBlock`。注意 slot 不等于连续产出块，可能有 skipped slot。

同步器建议：

- 维护本地 `slot -> blockhash,parentSlot,previousBlockhash,blockTime`。
- 使用 finalized commitment 扫描。
- 对 skipped slot 记录跳过或只推进高度指针，不应当成异常块。
- 对 RPC 返回 `Block not available`、历史裁剪、节点落后做重试和节点切换。
- 对每个交易解析 `meta.err`，失败交易不能入账。
- Token 充值优先基于 `preTokenBalances/postTokenBalances` 计算余额变化。

### 8.3 地址交易历史

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
address VARCHAR(120) NOT NULL
pubkey VARBINARY(65) 或 BLOB
derivation_path VARCHAR(120)
```

Solana 公钥 32 字节，地址为 base58。

### 14.2 Solana 交易表

```sql
CREATE TABLE `wallet_sol_txs` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY,
    `chain` VARCHAR(20) NOT NULL DEFAULT 'SOL',
    `signature` VARCHAR(128) NOT NULL COMMENT 'Solana 交易签名，等价交易ID',
    `slot` BIGINT NOT NULL DEFAULT 0,
    `block_hash` VARCHAR(128) NOT NULL DEFAULT '',
    `recent_blockhash` VARCHAR(128) NOT NULL DEFAULT '',
    `last_valid_block_height` BIGINT NOT NULL DEFAULT 0,
    `fee_payer` VARCHAR(120) NOT NULL DEFAULT '',
    `fee` VARCHAR(40) NOT NULL DEFAULT '0',
    `compute_units_consumed` BIGINT NOT NULL DEFAULT 0,
    `err` TEXT,
    `status` TINYINT NOT NULL DEFAULT 0 COMMENT '0=Pending 1=Confirmed 2=Finalized 3=Failed 4=Expired 5=Reverted',
    `rawtx` MEDIUMBLOB,
    `btime` DATETIME NULL,
    `ctime` DATETIME NOT NULL,
    `mtime` DATETIME NOT NULL,
    UNIQUE KEY `uk_signature` (`chain`, `signature`),
    KEY `idx_slot` (`chain`, `slot`),
    KEY `idx_status` (`chain`, `status`)
) ENGINE=InnoDB COMMENT='Solana 交易索引表';
```

### 14.3 Solana instruction 表

```sql
CREATE TABLE `wallet_sol_instructions` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY,
    `chain` VARCHAR(20) NOT NULL DEFAULT 'SOL',
    `signature` VARCHAR(128) NOT NULL,
    `slot` BIGINT NOT NULL DEFAULT 0,
    `instruction_index` INT UNSIGNED NOT NULL,
    `inner_index` INT UNSIGNED NOT NULL DEFAULT 0,
    `program_id` VARCHAR(120) NOT NULL,
    `parsed_type` VARCHAR(64) NOT NULL DEFAULT '',
    `source` VARCHAR(120) NOT NULL DEFAULT '',
    `destination` VARCHAR(120) NOT NULL DEFAULT '',
    `owner` VARCHAR(120) NOT NULL DEFAULT '',
    `mint` VARCHAR(120) NOT NULL DEFAULT '',
    `amount` VARCHAR(40) NOT NULL DEFAULT '0',
    `decimals` INT NOT NULL DEFAULT 0,
    `memo` VARCHAR(512) NOT NULL DEFAULT '',
    `is_ours` TINYINT NOT NULL DEFAULT 0,
    `ctime` DATETIME NOT NULL,
    UNIQUE KEY `uk_ix` (`chain`, `signature`, `instruction_index`, `inner_index`),
    KEY `idx_mint_dest` (`chain`, `mint`, `destination`),
    KEY `idx_slot` (`chain`, `slot`)
) ENGINE=InnoDB COMMENT='Solana 指令解析明细';
```

### 14.4 SPL Token 账户表

```sql
CREATE TABLE `wallet_sol_token_accounts` (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY,
    `chain` VARCHAR(20) NOT NULL DEFAULT 'SOL',
    `token_account` VARCHAR(120) NOT NULL,
    `owner_address` VARCHAR(120) NOT NULL COMMENT '用户主地址或系统地址',
    `uid` BIGINT NOT NULL DEFAULT 0,
    `mint` VARCHAR(120) NOT NULL,
    `token_program` VARCHAR(120) NOT NULL,
    `is_ata` TINYINT NOT NULL DEFAULT 1,
    `address_kind` TINYINT NOT NULL DEFAULT 0 COMMENT '0=User 1=Hot 2=Cold',
    `status` TINYINT NOT NULL DEFAULT 1 COMMENT '1=Active 0=Disabled',
    `ctime` DATETIME NOT NULL,
    `mtime` DATETIME NOT NULL,
    UNIQUE KEY `uk_token_account` (`chain`, `token_account`),
    UNIQUE KEY `uk_owner_mint_program` (`chain`, `owner_address`, `mint`, `token_program`),
    KEY `idx_uid` (`uid`, `chain`)
) ENGINE=InnoDB COMMENT='Solana SPL Token Account/ATA 归属表';
```

### 14.5 Solana Token 充值唯一键

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

建议新增 `09-sol-manager/demo/`，分阶段实现：

1. ed25519 地址派生和 base58 地址校验。
2. SOL transfer 交易构建、离线签名、签后反解析。
3. recent blockhash 过期处理和重签流程。
4. mock finalized block 解析，生成 SOL 充值记录。
5. SPL Token/ATA 归属表和 Token 充值解析。
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


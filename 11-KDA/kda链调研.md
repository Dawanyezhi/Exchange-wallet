# KDA 链接入调研

本文按交易所托管钱包视角调研 Kadena/KDA 接入：Chainweb 多链结构、Pact 账户模型、`k:` 账户、离线签名、交易构建、RPC、充值提现归集、对账、重组和当前生产上线风险。

结论先行：截至 2026-06-04，官方站点显示 Kadena LLC 已于 2025-10-21 结束业务运营；`chainweb-node` 官方仓库 README 还提示 Kadena Mainnet 于 2025-11-15T23:26:15Z 停止产块，并给出 final cut。若目标是交易所生产接入，当前不建议上线 KDA 充值/提现。下文仍保留技术接入方案，供教学、历史链解析或未来社区恢复网络后复核使用。

## 1. 基础结论

| 维度 | KDA 结论 | 钱包影响 |
|------|----------|----------|
| 当前状态 | 官方站点公告 Kadena LLC 停止运营；官方节点仓库提示 mainnet 停止产块 | 生产交易所不应新增上线；已有资产需进入只读、暂停充值提现、人工兑付/下架流程 |
| 链类型 | Chainweb 并行 PoW，多条链共享网络；每条 chain 上运行 Pact 合约状态 | 所有地址、余额、充值、提现、归集、确认数都必须带 `chainId`，不能只按单链账户处理 |
| 主网网络标识 | 历史主网 network id 为 `mainnet01`，chain id 为字符串 `"0"` 到 `"19"` | `networkId + chainId` 是防重放和路由核心字段 |
| 测试网 | 历史测试网常用 `testnet04` | 测试网和主网 key、数据库、RPC 必须隔离 |
| 原生币 | KDA，由 Pact `coin` 合约管理 | 充值解析不是原生余额差分接口，而是 Pact command/result/event/coin table 解析 |
| 精度 | KDA 通常按 12 位小数处理 | 内部金额必须用整数最小单位或 decimal string，禁止 `float64`；上线前复核当前 coin 合约精度 |
| 地址/账户 | 账户名是 Pact `coin` 表 row key；推荐 `k:<publicKey>` 账户；账户由 guard 控制 | “账户名”和“公钥/地址”不是同一概念；验址必须校验账户名、guard、公钥绑定关系 |
| 签名算法 | Ed25519，签名 Pact command hash | keyman 不能盲签 32 字节 hash，必须独立重建 command 并校验 meta/capability |
| 交易模型 | 无 EVM nonce；Pact command 有 `networkId`、`meta.chainId`、`creationTime`、`ttl`、`nonce`、signers/capabilities | 并发控制靠账户/chain 维度的本地发送锁、command hash/requestKey 幂等和 TTL 过期补偿 |
| RPC 能力 | Chainweb node 提供 `/cut`、Pact `/local`、`/send`、`/poll`、`/listen`、`/spv` 等 API | 生产需要自建节点和索引器；公共节点不适合作为记账唯一来源 |
| 费用模型 | Pact gas：`gasLimit * gasPrice`，由 meta.sender 支付 | 提现前必须 `/local` 预执行估 gas，并设置手续费上限 |
| Token/NFT | Pact 合约资产，历史上有 Marmalade NFT 等标准 | 首期即便网络恢复，也只建议 KDA 主币；Token/NFT 延后并做合约白名单 |
| Memo/Tag | 协议不强制 memo/tag | 建议用户独立 `k:` 账户；共享账户 + memo 不推荐 |
| finality | PoW 概率最终性；Chainweb 多链 cut 需要按目标 chain 深度确认 | 入账确认必须按 `chainId` 计算目标链高度差，并监控 cut 停滞 |

## 2. 官方资料确认的事实

官方站点当前显示 Kadena LLC 已于 2025-10-21 结束业务运营，并指向其 X 公告。`chainweb-node` 仓库 README 提示 Kadena Mainnet 已于 2025-11-15T23:26:15Z 停止产块，final cut 中 `instance` 为 `mainnet01`，包含 chain `"0"` 到 `"19"` 的最终高度和 hash。

官方文档站 sitemap 仍列出 `/api/pact-api/send`、`/api/pact-api/local`、`/api/pact-api/poll`、`/api/pact-api/listen`、`/api/pact-api/spv`、`/api/service-api/*`、`/reference/default-contracts/coin`、`/guides/transactions/*` 等页面，但当前部分页面请求会回退到文档首页壳，不能视为完整可用的生产文档。

`chainweb-node` README 说明 Chainweb 是并行 braided PoW 架构，节点服务 API 默认包含 `/info`、`/health-check`、Pact endpoints、headers、payloads、cuts 和 block header event stream；服务 API 默认端口示例为 1848，P2P 默认端口示例为 1789。README 也强调服务 API 对公网暴露时应加反向代理、限流、认证和 CORS 控制。

Kadena JavaScript crypto utils README 说明其 SDK 使用 `hash` 对 command payload 计算交易 hash，也能用私钥签名；公开函数包括 `hash`、`sign`、`signHash`、`verifySig` 等。工程上这验证了 KDA 签名对象是 Pact command，而不是 EVM RLP 交易或 BTC sighash。

参考资料：

- Kadena 官方站点停止运营公告：<https://www.kadena.io/>
- Chainweb node README：<https://github.com/kadena-io/chainweb-node>
- Chainweb node releases：<https://github.com/kadena-io/chainweb-node/releases>
- Kadena docs sitemap：<https://docs.kadena.io/sitemap.xml>
- Kadena docs 首页：<https://docs.kadena.io/>
- Kadena JS crypto utils：<https://github.com/kadena-community/kadena.js/tree/main/packages/libs/cryptography-utils>
- Kadena JS client：<https://github.com/kadena-community/kadena.js/tree/main/packages/libs/client>
- KIP-0012 `k:` account protocol：<https://github.com/kadena-io/KIPs/blob/master/kip-0012/kip-0012.md>

## 3. 链基础信息

历史技术信息：

- 主网 network id：`mainnet01`。
- 主网 chain id：`"0"` 到 `"19"`，共 20 条并行 chain。
- 测试网：历史常用 `testnet04`。
- 原生资产：KDA。
- 执行环境：Pact 智能合约；KDA 主币由 `coin` 合约管理。
- 共识：Chainweb PoW；多条链通过 parent header 引用形成 braided 结构。
- 节点客户端：`chainweb-node`。

工程建议：

- 数据库和业务参数必须把 `chain_id` 作为一等字段。`KDA:mainnet01:0` 和 `KDA:mainnet01:1` 是不同账本状态，不能合并余额。
- 充值地址分配应明确用户使用哪条 chain。用户向错误 chain 入金时，即使账户名相同，也不能直接按目标 chain 入账。
- 如果未来社区恢复网络，必须先确认当前 network id、可用 chain 数、节点版本、bootstrap 节点、浏览器、交易所流动性和重组历史，再重新评估上线。

## 4. 账户模型和钱包影响

KDA 不是 EVM Account 链，也不是 UTXO 链。KDA 主币余额存在 Pact `coin` 合约表中，账户名是表 key，账户行通常包含 balance 和 guard。guard 决定谁能花费账户余额。

对交易所钱包最重要的概念：

| 概念 | 说明 | 钱包影响 |
|------|------|----------|
| `account` | Pact coin account name，例如 `k:<pubkey>` 或自定义字符串 | 这是充值/提现目标，不等同于裸公钥 |
| `guard` | 控制账户的条件，常见 keyset guard 包含 keys 和 predicate | 验址必须校验 guard 是否符合预期 |
| `k:` account | KIP-0012 推荐账户名，把账户名绑定到单一公钥 | 首期建议只支持 `k:<64 hex pubkey>`，降低账户抢注和 guard 错配风险 |
| `chainId` | 交易执行的 Chainweb 子链 | 余额、交易、确认数、扫描游标全部按 chain 分片 |
| Pact command | 交易主体，包含 payload、meta、signers、networkId、nonce 等 | 签名和广播的核心对象 |
| requestKey | command hash，作为交易查询和幂等键 | 提现落库必须保存 requestKey |

对业务闭环的影响：

- 充值：按 `chainId` 扫描 Pact 执行结果和 `coin.TRANSFER` 事件，命中我方 account 后入账。
- 提现：构造 `coin.transfer` 或 `coin.transfer-create` Pact command；按 `from_account + chainId` 做本地并发锁。
- 归集：每个用户充值 account 在各 chain 上分别归集到热钱包或冷钱包 account。
- 对账：链上余额查询必须指定 chain；总余额是 20 条 chain 的资产汇总，不是单个账户查询。
- 回滚：PoW 重组时按 `networkId + chainId + height + blockHash` 回滚本地入账、事件和交易状态。

## 5. 地址和账户校验

KDA 的“地址”更准确说是 Pact account name。生产首期建议只支持 `k:` 账户：

```text
k:<64 lowercase hex ed25519 public key>
```

工程校验规则：

- account 必须以 `k:` 开头。
- `k:` 后必须是 32 字节 Ed25519 public key 的 64 位十六进制字符串。
- 入库统一小写；保留用户原始输入用于审计。
- 提现到已存在账户时，应通过 `/local` 查询或预执行确认目标账户 guard 与 `k:` 公钥一致。
- 提现到未存在账户时，使用 `coin.transfer-create` 并显式传入目标 guard，guard 必须只包含目标 `k:` 公钥和预期 predicate。
- 首期不支持自定义 account name、多签 guard、模块 guard、复杂 predicate、命名空间资产账户。

为什么不能只校验字符串：

- Pact account name 可以是任意字符串；如果允许非 `k:` 账户，账户名和控制公钥可能被分离。
- 自定义 account 可能已存在且 guard 不属于用户，错误提现会导致资产不可找回。
- 同一个 account name 在不同 chain 上是不同状态；必须同时校验 `chainId`。

数据库字段建议：

| 字段 | 建议 |
|------|------|
| account_name | `VARCHAR(256)`，保存规范化 account |
| public_key | `CHAR(64)`，仅 `k:` 账户可直接提取 |
| guard_json | `TEXT/JSON`，保存链上或构造时 guard |
| chain_id | `VARCHAR(8)`，取值 `"0"` 到 `"19"` |
| network_id | `VARCHAR(32)`，如 `mainnet01` |

## 6. 密码学算法和派生

KDA 普通签名使用 Ed25519。公钥通常表示为 32 字节 hex，签名通常为 64 字节 hex。

派生建议：

- 使用 SLIP-0010 Ed25519 硬化派生，不要复用 EVM secp256k1 BIP32 派生。
- 可参考 SLIP-0044 中 Kadena coin type，生产上线前必须复核当前标准编号并固化路径。
- 建议路径形态：

```text
m/44'/626'/account'/chain_index'/address_index'
```

其中 `chain_index` 可映射 Chainweb `chainId`，也可以所有 chain 复用同一公钥但必须在地址表中显式记录支持的 chain。交易所更推荐“同一用户同一 chain 一个 account”，方便对账和风险隔离。

签名服务要求：

- 不接受业务方直接传入 hash 盲签。
- keyman 应接收完整 Pact command 结构或 canonical command string。
- keyman 独立重算 command hash，校验 `networkId`、`meta.chainId`、`meta.sender`、gas、ttl、payload code、capability、amount、to/from。
- 签名审计记录 `business_id`、`account`、`chainId`、`networkId`、command hash、payload 摘要、capability 列表、key id、public key、signature。

## 7. 交易构建与签名

Pact command 的关键结构可以抽象为：

```json
{
  "payload": {
    "exec": {
      "code": "(coin.transfer \"k:from\" \"k:to\" 12.340000000000)",
      "data": {}
    }
  },
  "signers": [
    {
      "pubKey": "from_public_key_hex",
      "scheme": "ED25519",
      "clist": [
        {
          "name": "coin.TRANSFER",
          "args": ["k:from", "k:to", 12.34]
        },
        {
          "name": "coin.GAS",
          "args": []
        }
      ]
    }
  ],
  "meta": {
    "chainId": "1",
    "sender": "k:gas-payer",
    "gasLimit": 1000,
    "gasPrice": 0.00000001,
    "ttl": 600,
    "creationTime": 1760000000
  },
  "networkId": "mainnet01",
  "nonce": "withdraw:10010001"
}
```

签名前必须校验：

- `networkId` 与业务环境一致。
- `chainId` 是已启用 chain，且提现账户在该 chain 有余额。
- `meta.sender` 是我方热钱包/手续费账户，或者明确允许的 from account。
- `gasLimit * gasPrice` 不超过资产配置的手续费上限。
- `ttl` 合理，避免过短导致广播即过期，也避免过长造成失败不确定窗口。
- payload 只允许白名单函数：首期 `coin.transfer`、`coin.transfer-create`、只读查询。
- amount 是 decimal string 或最小单位整数转换结果，不能使用二进制浮点。
- signer capability 必须精确绑定 from、to、amount，不允许泛化 capability。
- 若使用 `transfer-create`，目标 guard 必须由目标 `k:` 公钥生成。

签后必须校验：

- 反解析 signed command，确认 payload/meta/signers 与待签对象一致。
- 本地用公钥验 Ed25519 签名。
- 重新计算 command hash/requestKey。
- 保存 raw command、requestKey、signature、signers、capability、业务单号。
- 先 `/local` 预执行，成功后 `/send` 广播；广播后用 `/poll` 或 `/listen` 查询。

### 7.1 生产案例：KDA 主币提现

场景：热钱包在 `mainnet01` 的 chain `"1"` 向外部 `k:` 账户提现 `12.34 KDA`。内部金额按 12 位小数处理，`12.34 KDA = 12340000000000` 最小单位。当前网络状态不建议真实广播，本案例用于技术方案说明。

业务请求：

```json
{
  "chain": "KDA",
  "network_id": "mainnet01",
  "chain_id": "1",
  "business_type": "withdraw",
  "business_id": "wd_10010001",
  "asset": {
    "symbol": "KDA",
    "decimals": 12,
    "contract": "coin"
  },
  "from_account": "k:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "to_account": "k:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "amount_base_units": "12340000000000",
  "fee_policy": {
    "gas_limit": 1000,
    "max_gas_price": "0.00000001",
    "max_fee_base_units": "10000000"
  }
}
```

链上状态输入由钱包服务读取：

```json
{
  "cut": {
    "network_id": "mainnet01",
    "chain_id": "1",
    "height": 6357350,
    "hash": "example-header-hash"
  },
  "from_account": {
    "account": "k:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "guard": {
      "keys": ["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"],
      "pred": "keys-all"
    },
    "balance_base_units": "500000000000000"
  },
  "to_account": {
    "account": "k:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    "exists": false,
    "expected_guard": {
      "keys": ["bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"],
      "pred": "keys-all"
    }
  }
}
```

unsigned command 关键字段：

```json
{
  "networkId": "mainnet01",
  "payload": {
    "exec": {
      "code": "(coin.transfer-create \"k:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\" \"k:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\" (read-keyset \"receiver-guard\") 12.340000000000)",
      "data": {
        "receiver-guard": {
          "keys": ["bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"],
          "pred": "keys-all"
        }
      }
    }
  },
  "signers": [
    {
      "pubKey": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "scheme": "ED25519",
      "clist": [
        {
          "name": "coin.TRANSFER",
          "args": [
            "k:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "k:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
            12.34
          ]
        },
        {
          "name": "coin.GAS",
          "args": []
        }
      ]
    }
  ],
  "meta": {
    "chainId": "1",
    "sender": "k:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "gasLimit": 1000,
    "gasPrice": 0.00000001,
    "ttl": 600,
    "creationTime": 1760000000
  },
  "nonce": "exchange-wallet:withdraw:wd_10010001"
}
```

说明：JSON 示例中的 `12.34` 仅表达 Pact decimal；Go 服务内部不得用 `float64` 生成该值，应由 `amount_base_units` 按 decimals 转成固定小数字符串 `12.340000000000`。

签名服务请求：

```json
{
  "key_id": "kda-hot-mainnet-chain1-000001",
  "derivation_path": "m/44'/626'/0'/1'/0'",
  "expected_public_key": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "network_id": "mainnet01",
  "chain_id": "1",
  "command": {
    "payload": "...完整 payload...",
    "signers": "...完整 signers...",
    "meta": "...完整 meta...",
    "nonce": "exchange-wallet:withdraw:wd_10010001"
  },
  "policy": {
    "allowed_functions": ["coin.transfer", "coin.transfer-create"],
    "allowed_capabilities": ["coin.TRANSFER", "coin.GAS"],
    "max_amount_base_units": "12340000000000",
    "max_fee_base_units": "10000000"
  }
}
```

签名服务响应：

```json
{
  "request_key": "base64url-command-hash",
  "public_key": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "signature": "ed25519_signature_hex",
  "hash_algorithm": "pact-command-hash",
  "signed_command": {
    "cmd": "canonical_command_string",
    "hash": "base64url-command-hash",
    "sigs": [{"sig": "ed25519_signature_hex"}]
  }
}
```

广播与落库：

1. 钱包服务对 signed command 做本地反解析、验签和手续费复算。
2. 调用目标 chain Pact `/local`，确认预执行成功、gas 在上限内。
3. 写入 `raw_transactions`：`business_id`、`network_id`、`chain_id`、`request_key`、`cmd`、`hash`、`status=SIGNED`。
4. 调用 `/send` 广播，状态置为 `BROADCASTED`。
5. 用 `/poll` 或 `/listen` 查询 requestKey，成功后记录 block height/hash、gas、result、events，提现置为 `CHAIN_SUCCESS`。
6. 若 TTL 过期且链上未出现 requestKey，释放本地账户发送锁，可构造新 nonce command 重发。
7. 若链上执行失败但已上链，手续费已消耗，提现置为 `CHAIN_FAILED`，不得自动释放为“未发生”。

## 8. RPC 与节点

历史 Chainweb/Pact API 形态：

```text
GET  /chainweb/0.0/{networkId}/cut
POST /chainweb/0.0/{networkId}/chain/{chainId}/pact/api/v1/local
POST /chainweb/0.0/{networkId}/chain/{chainId}/pact/api/v1/send
POST /chainweb/0.0/{networkId}/chain/{chainId}/pact/api/v1/poll
POST /chainweb/0.0/{networkId}/chain/{chainId}/pact/api/v1/listen
POST /chainweb/0.0/{networkId}/chain/{chainId}/pact/api/v1/spv
```

生产能力判断：

- `/cut` 用于获取多链最新 cut，判断节点是否同步、各 chain 最新高度和 hash。
- `/local` 用于只读查询和交易预执行，不应改变链上状态。
- `/send` 用于提交 command。
- `/poll` 批量查询 requestKey 状态。
- `/listen` 等待单个 requestKey 结果。
- `/spv` 用于跨链转账证明相关流程，首期不建议支持跨链转账。

工程建议：

- 生产必须自建至少两套 Chainweb node 或可信供应商节点，做高度/hash 差异检测。
- 需要单独索引器按 chain 拉取 block/payload/output/events，否则只靠 `/poll` 无法完整发现充值。
- 节点健康监控必须检查 `/cut` 总高度、每条 chain 高度、最新 block 时间、peer 数、Pact 服务可用性。
- 服务 API 不应裸露公网；若必须暴露，放在反向代理后并加鉴权、IP 白名单和限流。

## 9. 充值、提现、归集、对账设计

### 9.1 充值

推荐模式：每个用户、每条 chain 分配一个 `k:` account。

入账唯一键：

```text
network_id + chain_id + block_hash + request_key + event_index + asset_id + account
```

解析规则：

- 只解析确认深度达到配置的 block。
- 只处理成功执行的 Pact command。
- 只入账 `coin` 合约 KDA 主币，且事件/余额变更命中我方 account。
- `from == to`、金额为 0、失败 command、非白名单合约都不入账。
- 对 `transfer-create` 首次创建账户的入金，要同时校验新账户 guard 与我方 `k:` 公钥匹配。

### 9.2 提现

提现状态机建议：

```text
CREATED -> POLICY_CHECKED -> ACCOUNT_LOCKED -> SIGNING -> SIGNED
-> LOCAL_CHECKED -> BROADCASTED -> CHAIN_SUCCESS/CHAIN_FAILED/EXPIRED
```

并发控制：

- KDA 没有 EVM nonce，但同一 from account 并发发多笔会造成余额竞争、gas 竞争和业务顺序不确定。
- 建议按 `network_id + chain_id + from_account` 加本地发送锁。
- `nonce` 字段使用业务唯一值，保证 command hash 可审计，但不要把它当链上递增 nonce。

### 9.3 归集

- 按 chain 独立归集，不能从 chain `"1"` 直接普通转账到 chain `"2"` 的热钱包。
- 每条 chain 保留最小 gas 余额。
- 小额账户归集前计算 `amount - max_fee`，低于阈值不归集。
- 跨链归集需要 Pact cross-chain/SPV 流程，首期不建议支持。

### 9.4 对账

对账口径：

- 用户余额：按 `network_id + chain_id + account + asset_id` 汇总已确认入账和已成功提现。
- 链上余额：对每个我方 account 在每个 chain 查询 `coin.get-balance` 或等价只读调用。
- 热钱包余额：按 chain 分别核对，再汇总展示。
- 手续费：按链上 command result 的 gas 消耗核算，不使用预估费替代实际费。

## 10. 数据库表建议

| 表 | 关键字段 |
|----|----------|
| `kda_accounts` | `network_id`、`chain_id`、`account_name`、`public_key`、`guard_json`、`derivation_path`、`user_id`、`status` |
| `kda_blocks` | `network_id`、`chain_id`、`height`、`hash`、`parent_hashes_json`、`cut_id`、`block_time`、`status` |
| `kda_commands` | `request_key`、`network_id`、`chain_id`、`block_height`、`block_hash`、`cmd`、`result_status`、`gas`、`events_json` |
| `kda_transfers` | `request_key`、`event_index`、`from_account`、`to_account`、`amount_base_units`、`asset_id`、`direction`、`confirm_status` |
| `kda_withdrawals` | `business_id`、`from_account`、`to_account`、`chain_id`、`amount_base_units`、`fee_base_units`、`request_key`、`state` |
| `kda_account_locks` | `network_id`、`chain_id`、`account_name`、`business_id`、`locked_until`、`state` |
| `kda_reorg_logs` | `network_id`、`chain_id`、`old_hash`、`new_hash`、`height`、`affected_rows`、`handled_by` |

当前项目落地时，应在 `internal/coinset` 增加新的链类型，而不是把 KDA 塞进 EVM Account：

```go
KDA = Chain{
    Name:         "KDA",
    ChainID:      0, // 非 EVM chain id；真实 chainId 使用扩展字段保存 "0".."19"
    Type:         MultiChainAccount,
    Confirms:     120,
    Features:     FeatureSmartContract | FeatureMultiChain | FeaturePoW,
    NativeSymbol: "KDA",
    Decimals:     12,
}
```

现有 `coinset.Chain` 只有单个 `ChainID int64`，不足以表达 `networkId + 20 chainId`。建议新增扩展配置，例如：

```go
type ChainwebConfig struct {
    NetworkID string
    ChainIDs  []string
}
```

## 11. 费用模型

KDA 交易费用由 Pact gas 决定：

```text
fee = gas_used * gas_price
max_fee = gas_limit * gas_price
```

示例：

- `gasLimit = 1000`
- `gasPrice = 0.00000001 KDA`
- `maxFee = 0.00001 KDA`
- 若 KDA decimals=12，则 `maxFee = 10,000,000` 最小单位。

工程建议：

- 提现请求只传金额，不允许业务方传最终 gas 参数。
- 钱包服务根据链上 `/local` 预执行和本地配置选择 gas。
- 每个资产配置 `max_gas_limit`、`max_gas_price`、`max_fee_base_units`。
- 实际入账和财务核算以链上 result 中 gas 消耗为准。
- 小额归集阈值至少大于 `max_fee * 3`，否则容易产生经济性亏损。

## 12. 重组、finality 和停链风险

KDA/Chainweb 是 PoW 概率最终性，不是 BFT 确定性 finality。正常技术接入时需要：

- 按目标 `chainId` 计算确认深度：`current_chain_height - tx_block_height`。
- 保存 block hash，发现同高度 hash 变化时回滚。
- 对每条 chain 单独维护 safe height。
- 监控 `/cut` 是否持续前进；任一 chain 长时间停滞应告警。

确认数建议：

| 场景 | 建议 |
|------|------|
| demo | 6 blocks |
| 小额充值 | 60 blocks |
| 普通充值 | 120 blocks |
| 大额充值 | 240+ blocks + 人工复核 |
| 当前 2026 状态 | 不开放充值/提现 |

当前最重要的风险不是普通重组，而是网络停止产块和官方运营终止：

- 充值无法获得新确认。
- 提现无法确认或无法广播。
- 公共节点、浏览器、SDK、文档可能失去维护。
- 交易所上线后无法向用户提供可预期的链上提款服务。

## 13. Token、NFT、Memo/Tag 支持范围

首期建议：

- 只支持 KDA 主币。
- 只支持 `coin` 合约。
- 只支持 `k:` account。
- 不支持跨链转账。
- 不支持 Token、NFT、Marmalade、DEX LP、复杂 Pact 模块资产。
- 不支持共享账户 + memo。

原因：

- Pact 资产由不同合约定义，事件和权限模型差异大。
- Token 合约可能有自定义 transfer 逻辑、管理员权限、冻结/黑名单或升级风险。
- NFT/Marmalade 需要独立索引 token id、policy、sale 事件和所有权变化。
- 当前官方停止运营背景下，不应扩大资产面。

## 14. 生产安全清单

上线前必须满足：

- 重新确认 Kadena 网络是否恢复产块，官方/社区维护主体是否明确。
- 至少两个独立节点源返回一致 cut。
- 浏览器、节点、SDK、文档可用性满足运维要求。
- keyman 支持 Ed25519/SLIP-0010，且拒绝盲签 hash。
- 完成 `k:` account guard 校验测试。
- 完成 command canonical serialization、hash、签名、验签测试。
- 完成 `/local`、`/send`、`/poll`、`/listen` 集成测试。
- 完成 20 chain 扫描、回滚、safe height、余额汇总测试。
- 完成停链、节点落后、TTL 过期、链上失败、重复广播、重组回滚演练。
- 完成大额提现人工审批、手续费上限、风控暂停开关。

当前生产结论：不满足上线条件。

## 15. 对当前项目的落地建议

如果只是教学演示，可以在 `11-KDA` 后续新增两个 demo：

- `block-tx-parser`：读取固定样例 block/payload/output，解析 `coin.TRANSFER` 事件、requestKey、chainId、确认数。
- `tx-sign-send`：构造 `coin.transfer-create` command，做 Ed25519 签名、hash、签后反解析和 `/local` 预执行模拟。

如果要做生产能力，当前项目需要改造：

- `internal/coinset` 增加 Chainweb/MultiChainAccount 类型和 feature gate。
- 金额统一使用 `internal/bigint`，提供 12 位固定小数和 Pact decimal string 转换。
- keyman 增加 Ed25519 派生、Pact command hash、签名前策略校验。
- block sync 增加 `networkId + chainId` 维度。
- deposit 增加 Pact event 解析和 `k:` guard 校验。
- withdrawal 增加 command 状态机、TTL 过期补偿和 requestKey 幂等。
- reconciliation 增加 20 chain 余额汇总和节点 cut 差异告警。

## 16. 最终接入决策

回答调研模板要求：

- 是否建议接入：截至 2026-06-04，不建议生产接入。
- 首期最小支持范围：若未来恢复网络，只支持 KDA 主币、`mainnet01`、明确 chainId、`k:` account、普通同链 transfer/transfer-create。
- 不建议首期支持：跨链转账、Token、NFT、非 `k:` 账户、多签/复杂 guard、共享 memo、公共节点记账。
- 是否需要自建索引器：需要。只靠 `/poll` 不能完整发现充值。
- 充值确认数如何配置：普通技术方案建议 120 blocks 起，大额 240+ blocks；当前状态应全部暂停。
- 提现并发控制：按 `networkId + chainId + from_account` 本地锁，不是 EVM nonce。
- 回滚时恢复哪些表：blocks、commands、transfers、deposits、withdrawals、account balances、reconciliation snapshots、account locks。
- 对账口径：每条 chain 独立链上余额核对，再按 asset 汇总。
- 上线前必须补哪些测试：签名验签、Pact command canonicalization、20 chain 扫描、重组、TTL 过期、链上失败、停链暂停、guard 错配、金额精度。

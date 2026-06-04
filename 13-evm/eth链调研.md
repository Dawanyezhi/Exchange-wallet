# Ethereum / EVM 链接入调研

本文按交易所托管钱包视角调研 Ethereum 主网和通用 EVM 链接入，重点覆盖交易构建、手续费管理、扫链解析 ETH 原生转账和 ERC20 交易。首期范围建议限定为 Ethereum 主网 ETH + 白名单 ERC20；BSC、Polygon、Arbitrum、Optimism 等 EVM 链可复用大部分交易模型，但确认数、finality、手续费、trace 和 L2/桥风险必须按链单独配置。

本文明确区分：

- 官方资料确认的事实：来自 ethereum.org、Ethereum Execution APIs、EIP/ERC 标准、Ethereum Foundation 博客或客户端文档。
- 工程建议：基于交易所托管钱包生产经验，对充值、提现、归集、对账、风控和运维的落地建议。

## 1. 基础结论

| 维度 | Ethereum / EVM 结论 | 钱包影响 |
|------|---------------------|----------|
| 链类型 | Account + Nonce 链；主币和 Token 都在账户/合约状态中表达 | 提现并发核心是 `chain + from_address + nonce` 锁；扫链不能只按 UTXO 或余额差处理 |
| 共识/finality | Ethereum 主网为 PoS；存在 `latest`、`safe`、`finalized` 等区块标签 | 充值入账建议以 `safe/finalized` 或业务确认数滑动窗口为准；仍需 parentHash 重组检测 |
| 原生币精度 | ETH 最小单位 wei，`1 ETH = 10^18 wei` | 所有金额用整数 wei，禁止 `float64` |
| 地址体系 | 20 字节地址，常见 `0x` + 40 hex；ERC-55 支持大小写 checksum | 入库建议保存规范化小写地址和 checksum 展示地址；验址要校验 20 字节与 checksum |
| 签名算法 | ECDSA secp256k1；交易签名使用 Keccak-256 哈希 | 可复用 BIP32/secp256k1 keyman，但签名服务必须理解交易结构，不能盲签任意 hash |
| 派生路径 | 常见 BIP44 coin type 60：`m/44'/60'/account'/change/index` | 主网、测试网、不同 EVM 链的 key id/chain namespace 必须隔离 |
| 交易类型 | Legacy、EIP-2930、EIP-1559 type 0x02、EIP-4844 type 0x03、EIP-7702 type 0x04 | 首期提现建议只构建 EIP-1559 type 0x02；扫链必须兼容多交易类型 |
| 防重放 | EIP-155 / typed transaction 的 `chain_id` + account nonce | 签前和签后必须核对 chain id、nonce、from、to、value、data、gas |
| 费用模型 | EIP-1559：base fee + priority fee，实际费约 `gasUsed * effectiveGasPrice` | 手续费上限必须用 `gasLimit * maxFeePerGas` 控制；实际扣费按 receipt 复算 |
| Token | ERC20 最常见；ERC721/ERC1155/NFT 首期不建议支持 | ERC20 入账以合约地址 + Transfer event 为准，不信 symbol/name |
| Memo/Tag | 协议层没有强制 memo/tag | 建议每用户独立地址；不要用共享地址+memo 做 ETH/ERC20 首期充值 |
| RPC 能力 | 原生 JSON-RPC 支持区块、交易、receipt、logs、余额、广播、费用估算 | 原生节点不提供任意地址交易列表；生产需要自建扫块/日志索引 |
| 生产风险 | Nonce 卡单、gas 波动、失败交易日志、internal transfer 漏扫、代理/税费 Token、深度回滚、L2/桥差异 | 必须有 nonce 状态机、rawtx 反解析、receipt 二次校验、多节点比对和余额对账 |

## 2. 官方资料确认的事实

官方资料说明，Ethereum 有两类账户：Externally Owned Account 和 Contract Account。EOA 由私钥控制，可以发起交易；Contract Account 由代码控制，不能自己主动发起交易，只能响应调用。账户有地址、余额和 nonce；合约账户还有代码和 storage。

官方资料说明，Ethereum 交易字段包含发送方签名、接收方、value、data/input、gas limit、fee 参数和 nonce。交易被打包后，可通过交易 receipt 判断执行状态、实际 gas 消耗和 logs。EVM 合约事件会写入 transaction receipt 的 logs。

EIP-1559 官方标准说明，type 0x02 交易格式包含 `chain_id`、`nonce`、`max_priority_fee_per_gas`、`max_fee_per_gas`、`gas_limit`、`destination`、`amount`、`data`、`access_list` 和签名字段；签名哈希为 `keccak256(0x02 || rlp([...unsigned fields...]))`。base fee 由协议按区块拥堵程度调整并销毁，priority fee 支付给区块提议者。

ERC20 官方标准说明，`transfer(address,uint256)` 必须触发 `Transfer(address indexed from, address indexed to, uint256 value)` 事件；`name`、`symbol`、`decimals` 是可选方法；调用方必须处理返回 `false` 的情况，不能假设永远成功。

EIP-2718 官方标准说明 typed transaction 使用 `TransactionType || TransactionPayload`，不同交易类型由首字节区分。EIP-4844 引入 blob transaction type 0x03，主要服务 rollup 数据可用性。EIP-7702 引入 set-code transaction type 0x04，包含 authorization list，可让 EOA 设置委托代码。托管钱包首期不应主动构建 0x03/0x04 交易，但扫链和 raw tx 反解析必须能识别并保存其类型。

## 3. 链基础信息

官方资料确认：

- Ethereum 主网 chain id 为 `1`。
- 常见测试网包括 Sepolia、Holesky 等；测试网不能与主网共用 key namespace、RPC、数据库和风控配置。
- ETH 最小单位为 wei，`1 gwei = 10^9 wei`，`1 ETH = 10^18 wei`。
- Ethereum PoS slot 时间为 12 秒；一个 epoch 为 32 slots。
- JSON-RPC 支持 `latest`、`pending`、`safe`、`finalized` 等区块标签。客户端和供应商实现能力需要上线前实测。

工程建议：

- `coinset` 对 Ethereum 主网保持 `chain_id=1`、`type=Account`、`decimals=18`、`FeatureEIP1559`、`FeatureInternalTx`。
- 启动时调用 `eth_chainId` 校验 RPC chain id 与本地配置一致，广播前再次校验，防止把主网交易发到测试网或反向误发。
- 多 EVM 链不能只复用 `ETH` 配置。BSC、Polygon、Arbitrum、Optimism、Base 等链的确认数、费用参数、trace 能力、L1 fee、sequencer 风险、重组和桥资产模型都不同。
- 金额字段统一用最小单位整数字符串或 `math/big`/`internal/bigint`，展示精度只用于 UI/API 转换。

## 4. 账户模型和钱包影响

Ethereum 是 Account + Nonce 模型。

| 流程 | 设计要点 |
|------|----------|
| 地址分配 | 推荐每个用户每个链分配独立 EOA 充值地址；ERC20 可复用同一 EOA 地址 |
| ETH 充值 | 扫块识别成功上链交易中 `to` 命中我方地址且 `value > 0` 的原生转账 |
| ERC20 充值 | 扫 receipt/logs 中白名单 token 合约的 `Transfer` event，`to` 命中我方地址 |
| 提现 | 每个热钱包地址维护 nonce 锁；同一 nonce 同时只能绑定一个有效系统交易 |
| 归集 | 用户充值地址向热钱包转 ETH/ERC20；ERC20 归集地址必须有足够 ETH 支付 gas |
| 对账 | ETH 用 `eth_getBalance`；ERC20 用 `balanceOf(address)` 或自建索引余额，定期和本地流水核对 |
| 回滚 | 依赖 block number + block hash + parent hash；回滚充值、提现确认、系统交易和余额流水 |

与 UTXO 链的差异：

- EVM 没有 UTXO 选币和找零，余额是账户状态。
- 同一 `from` 地址交易必须按 nonce 顺序执行。低 nonce 卡住会阻塞后续高 nonce 交易。
- 加速/取消不是 RBF 标志，而是用相同 nonce、足够高 fee 的替换交易。
- Token 转账的 `tx.to` 是 token 合约地址，不是用户目标地址；真正的收款地址在 calldata 或 event log 中。

## 5. 地址、密钥和签名

### 5.1 地址体系

官方资料确认：

- EVM 地址为 20 字节，常见文本形式为 `0x` + 40 个十六进制字符。
- ERC-55 使用大小写混合 checksum，基于小写 hex 地址的 Keccak-256 结果决定字母大小写。
- 地址本身不携带网络前缀；同一个地址格式可出现在 ETH、BSC、Polygon 等不同 EVM 链。

工程建议：

- 数据库建议同时保存：
  - `address_lower`: 小写规范化地址，用于唯一索引和比较。
  - `address_checksum`: ERC-55 展示地址，用于 API 和审计。
  - `chain`: 链命名空间，不能只按 address 全局唯一。
- 提现验址：
  - 允许全小写或全大写地址，但推荐前端要求 checksum。
  - 如果输入是混合大小写，必须通过 ERC-55 checksum 校验。
  - 禁止 ENS 域名直接进入签名流程；如支持 ENS，应在业务层解析并冻结解析结果。
- 不要用 `eth_getCode(to) == 0x` 作为禁止/允许提现的唯一依据。合约钱包、多签、EIP-7702 委托账户会改变“EOA/合约”的边界；高风险合约地址可由风控单独拦截。

### 5.2 密钥和派生

Ethereum 使用 secp256k1 ECDSA。常见派生路径：

```text
m/44'/60'/account'/change/address_index
m/44'/60'/0'/0/0
```

工程建议：

- keyman 可复用 secp256k1 能力，但必须新增 EVM 交易结构级签名校验。
- 签名服务不能只接收“32 字节 hash 并签名”。至少要接收完整 unsigned transaction 结构或 canonical RLP/typed payload，并在签名前解析：
  - chain id
  - nonce
  - to
  - value
  - data
  - gas limit
  - max fee / priority fee / gas price
  - access list
  - 业务单号和资产配置
- 签名审计记录至少包含：`chain`、`network`、`chain_id`、`from`、`to`、`value`、`data_method`、`token_contract`、`token_to`、`token_amount`、`nonce`、`gas_limit`、`max_fee_per_gas`、`max_priority_fee_per_gas`、key id、公钥、签名结果、拒绝原因。

## 6. 交易类型和构建

### 6.1 常见交易类型

| 类型 | 说明 | 钱包建议 |
|------|------|----------|
| Legacy | RLP `[nonce, gasPrice, gasLimit, to, value, data, v, r, s]` | 老链或不支持 EIP-1559 的 EVM 链可保留；Ethereum 主网首期不主动使用 |
| EIP-2930 type 0x01 | Access list 交易 | 可解析保存；首期不主动构建 |
| EIP-1559 type 0x02 | 动态费用交易 | Ethereum 主网 ETH/ERC20 提现首选 |
| EIP-4844 type 0x03 | Blob transaction | 主要是 rollup 数据；托管钱包首期不主动构建，但扫链需兼容 |
| EIP-7702 type 0x04 | Set-code transaction，authorization list | 账号抽象相关；托管钱包首期不主动构建，扫链和风控要识别 |

### 6.2 ETH 原生转账

EIP-1559 ETH 转账 unsigned tx：

```text
type: 0x02
chain_id: 1
nonce: from address nonce
max_priority_fee_per_gas: tip cap
max_fee_per_gas: fee cap
gas_limit: 21000
to: recipient address
value: amount in wei
data: 0x
access_list: []
```

签名前必须校验：

- `chain_id` 与 `coinset` 配置一致。
- `from` 是系统热钱包或归集地址。
- `nonce` 来自本地 nonce 锁，不允许业务方传入。
- `to` 等于业务提现目标地址，且不在制裁/黑名单/冻结列表。
- `value` 等于业务金额，单位为 wei。
- `gas_limit` 对普通 ETH 转账为 21000；如果目标是合约地址，可能执行 fallback 并失败，业务上需决定是否允许。
- `gas_limit * max_fee_per_gas` 不超过手续费上限。
- 账户余额 >= `value + gas_limit * max_fee_per_gas`。

签后必须反解析 raw tx：

- 解析出 type 0x02 和所有字段。
- 恢复 signer 地址并核对 `from`。
- 重新计算 tx hash。
- 核对 `to/value/data/chain_id/nonce/gas` 与业务单一致。
- 将 rawtx、tx hash、nonce、fee caps、业务单号、签名审计落库。

### 6.3 ERC20 转账

ERC20 提现交易的 `to` 是 token 合约地址，`value` 通常为 `0`，真实收款地址和金额在 calldata：

```text
to: token_contract
value: 0
data: 0xa9059cbb
      + abi_encode(address recipient)
      + abi_encode(uint256 amount)
```

其中 `0xa9059cbb` 是 `transfer(address,uint256)` 的 4 字节 selector。

签名前必须校验：

- token 合约地址在白名单，按 `chain + contract_address` 唯一识别，不信 symbol。
- token decimals 来自可信配置或首次上链读取后冻结，不允许运行时被静默修改。
- `data` 只能是白名单方法，例如首期只允许 `transfer(address,uint256)`。
- calldata 中 recipient 等于业务提现目标地址。
- calldata 中 amount 等于 token 最小单位金额。
- `tx.value == 0`。
- gas limit 来自 `eth_estimateGas` + 安全余量，但不得超过 token 业务上限。
- 热钱包 ETH 余额足够支付 gas，token 余额足够支付提现金额。
- 对税费 Token、rebase Token、黑名单/暂停 Token、代理合约升级 Token 做 FeatureGate 或禁止首期支持。

签后必须反解析：

- 解析 raw tx，确认 `to=token_contract`、`value=0`。
- ABI 解码 calldata，确认 selector、recipient、amount。
- 恢复 signer 地址。
- 核对 chain id、nonce、gas fee caps。
- 不以签名前 `eth_call` 成功作为最终成功依据；最终以上链 receipt `status=1` 和 token Transfer event 校验。

## 7. Nonce 管理和状态机

EVM 提现生产稳定性很大程度取决于 nonce 管理。

### 7.1 Nonce 获取

RPC 的 `eth_getTransactionCount(address, "latest")` 返回已上链 nonce；`pending` 可能包含本节点 mempool 视图中的待确认交易，但多节点/供应商返回可能不一致。

工程建议：

- 初始化热钱包 nonce 时，用多节点查询 `latest` 和 `pending`，取可信结果并人工确认异常。
- 生产运行时以本地 nonce 表为准，通过数据库事务分配：

```text
evm_nonce_state {
  chain,
  from_address,
  next_nonce,
  latest_chain_nonce,
  pending_lowest_nonce,
  status,
  updated_at
}
```

- 分配 nonce 时对 `chain + from_address` 加行锁，插入系统交易唯一键 `chain + from_address + nonce`。
- 不要每笔提现直接查询 `pending` 后自增，RPC pending 视图不稳定会造成 nonce 重复或跳号。

### 7.2 系统交易状态

建议状态机：

```text
Created
  -> NonceLocked
  -> Built
  -> Signed
  -> Broadcast
  -> OnChainSuccess
  -> Finalized

Broadcast
  -> Replaced        // 同 nonce 加速或取消
  -> DroppedUnknown  // 长时间查不到，需谨慎重发同 nonce
  -> OnChainFailed   // receipt status=0
```

关键规则：

- `Signed/Broadcast` 后不能释放 nonce 给其他业务单。
- 同 nonce 替换交易必须仍绑定同一业务单或明确记录为 cancel/fee bump 系统交易。
- 同 nonce 加速时，用户提现目标输出不得改变；只允许提高 fee caps。
- 取消交易是向自己转 0 ETH 的同 nonce 交易，只能在业务确认取消、且原交易未上链时使用。
- 一旦某个 nonce 上链成功或失败，后续 nonce 才能继续正常确认。

## 8. 手续费管理

### 8.1 EIP-1559 费用结构

官方标准确认：

- base fee 是协议按区块使用量动态调整的网络费，会被销毁。
- priority fee 是给区块提议者的小费。
- 交易设置 `maxFeePerGas` 和 `maxPriorityFeePerGas`。
- 实际支付 gas price 可通过 receipt 的 `effectiveGasPrice` 获取。

工程公式：

```text
fee_upper_bound = gas_limit * max_fee_per_gas
actual_fee = gas_used * effective_gas_price
effective_gas_price <= max_fee_per_gas
```

对 ETH 提现，账户签前余额至少应满足：

```text
balance_wei >= transfer_value_wei + gas_limit * max_fee_per_gas
```

对 ERC20 提现，账户签前 ETH 余额至少应满足：

```text
eth_balance_wei >= gas_limit * max_fee_per_gas
token_balance >= token_amount
```

### 8.2 费用估算流程

建议流程：

1. `eth_feeHistory` 获取最近区块 base fee、reward 分位数，估算 priority fee。
2. `eth_maxPriorityFeePerGas` 可作为辅助，不作为唯一来源。
3. `eth_estimateGas` 对具体交易估算 gas limit。
4. 设置业务级上限：
   - ETH 普通转账 gas limit 固定 21000。
   - ERC20 常见可从估算值加 20%-40% buffer，但必须有 token 上限，例如 200000 或按 token 配置。
   - `maxFeePerGas` 不得超过链级上限和风控上限。
5. 签名前做余额和 fee cap 校验。

示例：

```text
base_fee_next_estimate = 30 gwei
priority_fee = 2 gwei
max_fee_per_gas = 2 * base_fee_next_estimate + priority_fee = 62 gwei

ETH transfer:
gas_limit = 21000
fee_upper_bound = 21000 * 62 gwei = 1,302,000 gwei = 0.001302 ETH

ERC20 transfer:
eth_estimateGas = 51,000
gas_limit = 65,000
fee_upper_bound = 65,000 * 62 gwei = 0.00403 ETH
```

### 8.3 加速和替换

工程建议：

- 对同 nonce 交易发起 replacement，新的 fee 必须满足客户端 mempool replacement 规则。不同客户端/供应商要求可能不同，生产建议至少提高 10%-15%，并同时提高 `maxFeePerGas` 和 `maxPriorityFeePerGas`。
- 加速交易必须复用相同 nonce，且业务输出不变：
  - ETH 提现：`to/value/data` 不变。
  - ERC20 提现：`to=token_contract`、`data=transfer(recipient, amount)` 不变。
- 如果 base fee 已经高于旧交易 `maxFeePerGas`，应重新计算新 `maxFeePerGas`，避免继续不可上链。
- 替换广播后，rawtx 表要保存 replacement 链路：`replaces_tx_hash`、`replacement_reason`、`old_fee_caps`、`new_fee_caps`。

## 9. 扫链和交易解析

### 9.1 扫链入口

推荐生产扫链以区块为主：

1. `eth_getBlockByNumber(height, true)` 拉区块头和完整交易。
2. 保存 `height/hash/parentHash/timestamp`。
3. 对每笔交易拉 `eth_getTransactionReceipt(tx_hash)`，或使用批量 RPC。
4. 解析 ETH 原生转账、ERC20 logs、提现回执和系统交易状态。
5. 用 `parentHash` 检测重组，回滚到共同祖先。

不要依赖“按地址交易列表”作为主记账来源。官方 JSON-RPC 不提供任意地址历史列表；Etherscan、Blockscout、第三方 indexer 只能作为运营查询、补偿和二次校验，不能替代自建扫块/扫日志。

### 9.2 ETH 原生转账解析

入账条件建议：

- 交易所在区块已进入安全高度。
- receipt `status=1`。
- transaction `to` 命中我方地址。
- transaction `value > 0`。
- `input == 0x` 可作为普通 ETH 转账特征，但不能把 `input != 0x` 的交易一概忽略；合约也可能向我方地址转 ETH，需要 internal tx/trace 覆盖。

普通 ETH 充值唯一键：

```text
chain + tx_hash + native_transfer_index
```

其中普通外层转账可令 `native_transfer_index=0`。如果接入 trace/internal transfer，则同一 tx 内可能有多笔 ETH 内部转账，必须用 trace path 或自定义 index 区分。

### 9.3 ERC20 充值解析

ERC20 入账以 receipt logs 为准：

```text
log.address == token_contract_whitelist
log.topics[0] == keccak256("Transfer(address,address,uint256)")
log.topics[1] == from
log.topics[2] == to
log.data == uint256 amount
receipt.status == 1
```

入账条件建议：

- token 合约在白名单，按 chain 维度配置。
- receipt `status=1`。失败交易即使有调用意图，也不得入账。
- log 没有被 removed；如果用 filter/subscription，要处理 `removed=true`。
- `to` 命中我方地址。
- `amount > 0`。ERC20 标准允许 0 金额 Transfer event，生产可记录但不入账，避免垃圾流水。
- 入账金额以 event 的 `value` 为准，不以 calldata amount 为准。税费/反射/rebase Token 可能导致实际到账与调用参数不同。

ERC20 入账唯一键：

```text
chain + tx_hash + log_index
```

必须保存：

- `contract_address`
- `from_address`
- `to_address`
- `amount`
- `decimals_snapshot`
- `block_number`
- `block_hash`
- `tx_index`
- `log_index`
- `event_signature`

### 9.4 提现交易解析

提现状态更新不能只看交易 hash 存在，要看 receipt：

- `receipt == nil`：未上链或 RPC 未同步。
- `receipt.status=1`：执行成功。
- `receipt.status=0`：执行失败，gas 已消耗，业务资产未按预期转出。
- `receipt.blockHash` 与本地块表不一致：可能在不同分叉，需等待重扫或回滚。

ETH 提现成功确认：

- tx hash 属于本系统 rawtx。
- receipt `status=1`。
- 反查 transaction：`from/to/value/nonce` 与系统交易一致。
- actual fee = `receipt.gasUsed * receipt.effectiveGasPrice`。

ERC20 提现成功确认：

- receipt `status=1`。
- transaction `to` 是 token 合约，`value=0`。
- calldata 是 `transfer(recipient, amount)`。
- receipt logs 中存在白名单 token 合约发出的 `Transfer(from=hot_wallet, to=recipient, value=amount)`。
- 如果 token 是税费/异常 Token，可能 `Transfer` value 与请求 amount 不一致，首期应禁止或按 token 独立策略处理。

### 9.5 Internal ETH 转账

普通 JSON-RPC receipt logs 不包含 ETH internal transfer。合约通过 `CALL`、`SELFDESTRUCT` 等路径向我方地址转 ETH 时，外层 transaction `to` 可能不是我方地址。

工程建议：

- 首期如果只支持用户直接向充值地址转 ETH，可在产品/API 明确“不自动识别合约内部转账充值”，并提供人工补录流程。
- 生产交易所通常需要支持 internal ETH 充值识别，否则用户从交易所、合约钱包、DeFi 协议转入可能漏记。
- 支持 internal tx 需要 trace 能力，例如 Geth `debug_traceBlockByNumber` / `debug_traceTransaction`，Erigon/Nethermind/Besu 的 trace 能力和返回结构不同，需要按客户端适配。
- trace 不应作为单节点单一信任来源。建议至少自建 trace 节点 + 普通执行节点交叉校验，或自建索引器。

### 9.6 失败交易和假充值防护

必须过滤：

- receipt `status=0` 的失败交易。
- 非白名单 token 合约伪造的 `Transfer` 事件。
- 只看 calldata 的假充值。例如用户调用某个合约，calldata 里出现我方地址，但并没有 token Transfer event。
- `transferFrom`、`approve`、DEX swap 等非直接充值意图，只有最终 receipt logs 中我方地址收到白名单 token 才可入账。
- 同名 symbol 假币。symbol/name 不参与入账身份判断。
- 合约创建交易 `to=null` 中的事件，需要按 logs 解析，不应按 tx.to。

## 10. RPC 与节点部署

| 场景 | RPC/接口 | 官方支持 | 是否需额外索引 | 生产使用建议 |
|------|----------|----------|----------------|--------------|
| 校验链 | `eth_chainId` | 支持 | 否 | 启动、广播前、定时巡检都要校验 |
| 同步状态 | `eth_syncing`、`eth_blockNumber` | 支持 | 否 | 节点落后超过阈值暂停入账/出账 |
| 最新区块 | `eth_blockNumber`、`eth_getBlockByNumber` | 支持 | 否 | 扫块主入口 |
| 安全区块 | `eth_getBlockByNumber("safe")`、`"finalized"` | 支持但需实测客户端/供应商 | 否 | 主网入账可结合 safe/finalized |
| 区块详情 | `eth_getBlockByNumber(height, true)` | 支持 | 否 | 拉完整交易；大块需批量和超时控制 |
| 交易详情 | `eth_getTransactionByHash` | 支持 | 否 | 广播和提现补偿 |
| Receipt | `eth_getTransactionReceipt` | 支持 | 否 | 判断成功/失败、gas、logs |
| Logs | `eth_getLogs` | 支持 | 不需但有范围限制 | 可按 token/topic 批量扫；注意查询跨度和漏扫 |
| ETH 余额 | `eth_getBalance` | 支持 | 否 | 对账 |
| ERC20 余额 | `eth_call balanceOf(address)` | 支持 | 否 | 对账；需 ABI 和合约白名单 |
| 地址交易历史 | 无通用官方接口 | 不支持 | 是 | 自建索引器；第三方只辅助 |
| 广播交易 | `eth_sendRawTransaction` | 支持 | 否 | 必须保存 rawtx 和错误码 |
| 估算 gas | `eth_estimateGas` | 支持 | 否 | 估算失败要区分余额不足、合约 revert、RPC 问题 |
| 费用估算 | `eth_feeHistory`、`eth_maxPriorityFeePerGas`、`eth_gasPrice` | 支持 | 否 | EIP-1559 链优先 feeHistory |
| Mempool | `txpool_*` | 客户端特有 | 否 | 仅内网自建节点使用，不依赖供应商 |
| Trace | `debug_trace*`、`trace_*` | 客户端/配置相关 | 需要 trace 节点 | internal ETH 充值、复杂合约分析需要 |

节点部署建议：

- 至少两套独立 RPC：普通执行节点 + 备份节点；如支持 internal transfer，另建 trace 节点。
- RPC 只在内网暴露，启用鉴权、IP 白名单、限流和审计。
- 扫链、广播、对账尽量使用不同 RPC 连接池，避免大查询影响出金。
- 对归档能力做明确选择：普通 ETH/ERC20 近期扫链不一定需要 archive，但历史重扫、任意旧高度 `eth_call`、审计补偿可能需要 archive 或自建索引快照。
- 节点落后、返回链 ID 不一致、parentHash 不连续、receipt 缺失率异常、`safe/finalized` 停滞都要告警并触发暂停策略。

## 11. 数据模型建议

在项目通用表基础上，EVM 建议新增或扩展：

```text
evm_blocks
  chain
  height
  hash
  parent_hash
  timestamp
  base_fee_per_gas
  gas_used
  gas_limit
  finalized_status

evm_nonce_state
  chain
  from_address
  next_nonce
  latest_chain_nonce
  status
  updated_at

evm_raw_transactions
  chain
  business_id
  business_type
  from_address
  nonce
  tx_hash
  raw_tx
  tx_type
  to_address
  value_wei
  data_hash
  gas_limit
  max_fee_per_gas
  max_priority_fee_per_gas
  effective_gas_price
  gas_used
  status
  replaces_tx_hash
  created_at
  updated_at

evm_token_contracts
  chain
  contract_address
  symbol
  decimals
  standard
  risk_flags
  status
  decimals_source
  audited_at

evm_logs
  chain
  block_number
  block_hash
  tx_hash
  tx_index
  log_index
  contract_address
  event_signature
  topic1
  topic2
  topic3
  data
  removed

evm_token_transfers
  chain
  tx_hash
  log_index
  block_number
  block_hash
  contract_address
  from_address
  to_address
  amount
  uid
  direction
  status

evm_internal_transfers
  chain
  tx_hash
  trace_address
  block_number
  block_hash
  from_address
  to_address
  value_wei
  call_type
  status
```

关键唯一键：

- `evm_blocks`: `chain + height`，`chain + hash`
- `evm_raw_transactions`: `chain + from_address + nonce`，`chain + tx_hash`
- `evm_logs`: `chain + tx_hash + log_index`
- `evm_token_transfers`: `chain + tx_hash + log_index`
- `evm_internal_transfers`: `chain + tx_hash + trace_address`

余额流水必须可重建余额，不能只依赖余额快照。

## 12. 重组、finality 和回滚

Ethereum PoS 主网通常重组深度较小，但交易所钱包不能假设永不重组。工程上仍需保存区块头并按 parentHash 检测。

建议确认策略：

| 业务 | 建议 |
|------|------|
| demo | 可用 1-3 个确认展示 |
| 小额 ETH/ERC20 | 可用 `safe` 或 12 blocks 级别策略，按风控调整 |
| 普通金额 | 建议进入 safe/finalized 或更保守的本地确认数 |
| 大额充值 | finalized 后再加风控延迟或人工复核 |
| L2/EVM 侧链 | 不套用 Ethereum 主网确认数，按链单独评估 |

回滚流程：

1. 发现 `new_block.parent_hash != local_tip.hash`。
2. 向前查找共同祖先。
3. 在数据库事务中按高度倒序回滚：
   - 标记该高度充值为 Reverted。
   - 回滚余额流水。
   - 提现从 Finalized/OnChainSuccess 回到 OnChainPending 或 Reorged，等待重扫确认。
   - 系统交易、归集交易回到待确认状态。
   - 删除或标记 `evm_logs`、`evm_token_transfers`、`evm_internal_transfers`。
   - 删除或标记区块头。
4. 从共同祖先后重新扫块。
5. 超过阈值的深度重组触发暂停入账/出账和人工介入。

## 13. 生产案例：ERC20 提现构建与离线签名

场景：Ethereum 主网热钱包向外部用户地址提现 100 USDC。假设 USDC decimals=6，金额为 `100000000` 最小单位。示例合约地址和地址仅用于说明，生产必须使用真实白名单配置。

### 13.1 业务请求

```json
{
  "chain": "ETH",
  "network": "mainnet",
  "business_type": "withdraw",
  "business_id": 202606040001,
  "asset": {
    "symbol": "USDC",
    "contract": "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
    "decimals": 6
  },
  "from_address": "0x1111111111111111111111111111111111111111",
  "to_address": "0x2222222222222222222222222222222222222222",
  "amount": "100000000",
  "fee_policy": {
    "mode": "eip1559",
    "max_fee_per_gas_limit": "100000000000",
    "max_total_fee_wei": "10000000000000000"
  }
}
```

### 13.2 链上状态输入

钱包服务从可信 RPC 和本地库获取：

```json
{
  "chain_id": 1,
  "local_nonce_lock": {
    "from_address": "0x1111111111111111111111111111111111111111",
    "nonce": 128
  },
  "eth_balance_wei": "3000000000000000000",
  "token_balance": "5000000000",
  "fee_quote": {
    "base_fee_per_gas": "30000000000",
    "max_priority_fee_per_gas": "2000000000",
    "max_fee_per_gas": "62000000000"
  },
  "estimate_gas": "51000",
  "gas_limit": "65000"
}
```

### 13.3 Unsigned tx

`transfer(address,uint256)` calldata：

```text
selector = 0xa9059cbb
recipient = 0000000000000000000000002222222222222222222222222222222222222222
amount    = 0000000000000000000000000000000000000000000000000000000005f5e100
data      = 0xa9059cbb00000000000000000000000022222222222222222222222222222222222222220000000000000000000000000000000000000000000000000000000005f5e100
```

unsigned tx：

```json
{
  "type": "0x02",
  "chain_id": "0x1",
  "nonce": "0x80",
  "max_priority_fee_per_gas": "0x77359400",
  "max_fee_per_gas": "0xe6e492e00",
  "gas_limit": "0xfde8",
  "to": "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
  "value": "0x0",
  "data": "0xa9059cbb00000000000000000000000022222222222222222222222222222222222222220000000000000000000000000000000000000000000000000000000005f5e100",
  "access_list": []
}
```

### 13.4 签名前策略校验

钱包服务和签名服务都必须校验：

- `chain_id=1`。
- `from_address` 是系统热钱包，key id 与地址派生记录一致。
- nonce=128 已由数据库锁定，且 `chain + from + nonce` 没有其他有效交易。
- token 合约在 USDC 白名单中，decimals=6，risk flags 允许提现。
- `to` 是 token 合约，不是用户地址。
- `value=0`。
- calldata selector 是 `transfer(address,uint256)`。
- calldata recipient 等于业务提现目标地址。
- calldata amount 等于 `100000000`。
- `gas_limit * max_fee_per_gas = 65000 * 62000000000 = 4030000000000000 wei`，低于业务手续费上限。
- 热钱包 ETH 余额足够支付 fee upper bound，USDC 余额足够支付提现金额。
- 交易中没有额外未知 data、access list 或 authorization list。

### 13.5 签名服务入参/出参

签名服务请求：

```json
{
  "request_id": "sign-202606040001",
  "chain": "ETH",
  "chain_id": 1,
  "key_id": "hot-eth-mainnet-001",
  "from_address": "0x1111111111111111111111111111111111111111",
  "derivation_path": "m/44'/60'/0'/0/12",
  "unsigned_tx": {
    "type": "0x02",
    "nonce": "0x80",
    "to": "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
    "value": "0x0",
    "data": "0xa9059cbb...",
    "gas_limit": "0xfde8",
    "max_fee_per_gas": "0xe6e492e00",
    "max_priority_fee_per_gas": "0x77359400",
    "access_list": []
  },
  "business_assertions": {
    "asset_contract": "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
    "erc20_method": "transfer",
    "recipient": "0x2222222222222222222222222222222222222222",
    "amount": "100000000"
  }
}
```

签名服务内部重算：

```text
signing_payload = 0x02 || rlp([
  chain_id,
  nonce,
  max_priority_fee_per_gas,
  max_fee_per_gas,
  gas_limit,
  destination,
  amount,
  data,
  access_list
])
signing_hash = keccak256(signing_payload)
signature = secp256k1_ecdsa(signing_hash)
```

响应：

```json
{
  "request_id": "sign-202606040001",
  "result": "approved",
  "signing_hash": "0x...",
  "signature": {
    "y_parity": "0x0",
    "r": "0x...",
    "s": "0x..."
  },
  "raw_tx": "0x02f8...",
  "tx_hash": "0x..."
}
```

### 13.6 签后校验和广播

广播前钱包服务必须：

- 反解析 raw tx，确认所有 unsigned 字段和签名字段。
- 本地恢复 signer 地址，确认等于 `from_address`。
- 重新计算 tx hash，与签名服务返回一致。
- ABI 解码 calldata，再次核对 token recipient 和 amount。
- 写入 `evm_raw_transactions`，状态 `Signed`。
- 调用 `eth_sendRawTransaction(raw_tx)`。
- 广播成功或 already known 等幂等错误后，状态转 `Broadcast`。

上链后：

- 扫描 receipt。
- 若 `status=1`，查找 `Transfer(from=hot_wallet, to=recipient, value=100000000)`。
- 计算实际手续费 `gasUsed * effectiveGasPrice`。
- 更新提现状态 `OnChainSuccess`，进入安全高度后转 `Finalized`。
- 若 `status=0`，状态转 `OnChainFailed`，记录 gas 损失，不把用户提现标记成功。

### 13.7 卡单处理

- 如果交易长时间未上链，先查多节点 `eth_getTransactionByHash`、`eth_getTransactionCount(latest/pending)`、本地 nonce 状态。
- 需要加速时，构建同 nonce、同输出的新交易，只提高 fee caps。
- 需要取消时，必须经过业务审批，构建同 nonce 自转 0 ETH 取消交易；原提现单不得自动成功。
- 如果原交易和替换交易在不同节点返回不一致，暂停该热钱包后续 nonce 广播，直到确认哪笔交易上链。

## 14. 归集设计

ETH 归集：

- 用户地址有 ETH 余额后，可转到热/冷钱包。
- 归集前保留最低 gas 预算，避免余额不足导致失败。
- 小额 ETH 归集要评估手续费经济性，低于阈值冻结或批量等待。

ERC20 归集：

- 用户地址收到 ERC20 后，需要 ETH 支付 gas 才能把 token 转出。
- 需要补 gas 流程：
  - 热钱包向用户地址补少量 ETH。
  - 用户地址发起 ERC20 transfer 到归集地址。
  - 剩余 ETH 可视金额回收或保留。
- 补 gas 必须有额度、频率和地址归属校验，防止向非系统地址补费。
- 税费、黑名单、暂停、代理升级 Token 不应使用通用归集策略。

## 15. Token、NFT、Memo/Tag 支持范围

首期建议：

- 支持 ETH。
- 支持白名单 ERC20，按 `chain + contract_address` 配置。
- 不支持 ERC721/ERC1155/NFT 自动入账、提现和归集。
- 不支持用户共享地址 + memo。
- 不支持 approve/permit/permit2/DEX 交互作为托管钱包出金能力。
- 不主动构建 EIP-7702、EIP-4337 UserOperation、blob transaction。

Token 风险分类：

| 风险 | 影响 | 建议 |
|------|------|------|
| decimals 可选或返回异常 | 展示/金额换算错误 | 上线前人工审计并冻结 decimals |
| symbol/name 重名 | 假币入账 | 只信合约地址 |
| fee-on-transfer | 提现金额和实际到账不一致 | 首期禁用或独立 FeatureGate |
| rebase/reflection | 余额会非交易变化 | 首期禁用或独立对账逻辑 |
| blacklist/pause | 提现/归集可能失败 | 风控标记，必要时禁用 |
| proxy upgrade | 行为可能变化 | 监控 implementation 变更和合约事件 |
| non-standard return | `transfer` 不返回 bool 或返回 false | SDK/签后逻辑按 receipt + event 判断，不只看 eth_call |

## 16. 生产安全清单

密钥与签名：

- 禁止打印私钥、助记词、seed、chain code。
- keyman 只返回签名/raw tx，不暴露私钥。
- 签名服务必须做结构化交易解析和策略校验。
- 大额提现进入多签/审批/延迟队列。

交易构建：

- `chain_id`、nonce、fee caps、to/value/data 都要签前签后双校验。
- ERC20 合约和 selector 必须白名单。
- 手续费上限必须按 `gas_limit * max_fee_per_gas`。
- raw tx 广播前必须落库。

充值：

- receipt `status=1` 才能入账。
- ERC20 按 `contract + Transfer event + log_index` 入账。
- 保存 block hash，处理 reorg。
- internal ETH 充值如果未支持，必须明确产品限制和人工流程。

提现：

- nonce 表必须事务锁定。
- 同 nonce 只允许一个有效业务输出。
- 替换交易必须保留原业务输出或明确 cancel。
- 失败交易消耗 gas，不得误判为成功提现。

对账：

- 定时 `eth_getBalance` 对账 ETH。
- 定时 `balanceOf` 对账 ERC20。
- 本地余额必须能由流水重建。
- 对账差异进入告警、冻结出金、人工处理。

运维：

- 多 RPC、多节点比对。
- 节点落后、safe/finalized 停滞、receipt 缺失、重组超阈值告警。
- 外部浏览器/API 只能辅助，不作为唯一记账源。

## 17. 对当前项目的落地改造建议

当前 `internal/coinset` 已有 EVM 链基础配置：

```go
ETH = Chain{
    Name:         "ETH",
    ChainID:      1,
    Type:         Account,
    Confirms:     12,
    Features:     FeatureInternalTx | FeatureEIP1559,
    NativeSymbol: "ETH",
    Decimals:     18,
}
```

建议后续改造：

- 新增 `13-evm/tx-build-sign` demo：
  - 构建 EIP-1559 ETH transfer。
  - 构建 ERC20 transfer calldata。
  - 签前策略校验。
  - secp256k1 签名和 raw tx 反解析。
  - nonce lock 模拟。
- 新增 `13-evm/block-tx-parser` demo：
  - 解析区块交易。
  - 解析 receipt logs。
  - 识别 ETH 原生充值。
  - 识别 ERC20 Transfer event。
  - 过滤失败交易和非白名单 token。
  - 模拟 reorg 回滚。
- 新增测试：
  - ERC-55 验址。
  - EIP-1559 signing hash 和 signer recovery。
  - ERC20 calldata encode/decode。
  - receipt status=0 不入账。
  - 同 tx 多 logs 按 log_index 去重。
  - nonce 重复分配被唯一键拦截。
  - replacement fee bump 保持业务输出不变。
- 数据模型按 `evm_blocks`、`evm_raw_transactions`、`evm_logs`、`evm_token_transfers`、`evm_nonce_state` 扩展。
- 文档中必须持续声明 demo 与生产差距，尤其是 RPC 多节点、trace、风控、HSM/MPC、审批、灾备和对账能力。

## 18. 是否建议接入

建议接入 Ethereum 主网 ETH + 白名单 ERC20。Ethereum/EVM 是交易所钱包必须支持的核心链型，但生产实现复杂度不能低估：Nonce 卡单、费用波动、ERC20 非标准行为、internal transfer、重组和 L2 差异都是高频风险。

最小上线能力：

- ETH 充值、提现、归集、余额对账。
- 白名单 ERC20 充值、提现、归集、余额对账。
- EIP-1559 type 0x02 交易构建和签名。
- nonce 锁和替换交易状态机。
- 扫块 + receipt logs 解析。
- parentHash 重组检测和回滚。
- 多节点链 ID/高度/区块哈希校验。

首期不建议支持：

- NFT/ERC721/ERC1155。
- internal ETH 自动入账，除非已经具备 trace 节点和索引能力。
- 税费 Token、rebase Token、黑名单/暂停高风险 Token。
- EIP-7702 主动发起、EIP-4337 AA、permit/permit2/approve 代签。
- L2/桥资产与 Ethereum 主网混用同一确认策略。

必须自建索引器：

- 对 ETH/ERC20 生产入账，至少要自建扫块和 logs 索引。
- 如果支持 internal ETH 充值，必须自建 trace 索引或等价能力。
- 第三方浏览器/API 不应作为唯一账务来源。

## 19. 参考资料

- Ethereum Accounts: <https://ethereum.org/en/developers/docs/accounts/>
- Ethereum Transactions: <https://ethereum.org/en/developers/docs/transactions/>
- Ethereum Gas and Fees: <https://ethereum.org/en/developers/docs/gas/>
- Ethereum JSON-RPC API: <https://ethereum.org/en/developers/docs/apis/json-rpc/>
- Ethereum Execution APIs: <https://ethereum.github.io/execution-apis/api-documentation/>
- EIP-20 / ERC20 Token Standard: <https://eips.ethereum.org/EIPS/eip-20>
- EIP-55 / ERC-55 checksum address: <https://eips.ethereum.org/EIPS/eip-55>
- EIP-155 / Simple replay attack protection: <https://eips.ethereum.org/EIPS/eip-155>
- EIP-1559 / Fee market change: <https://eips.ethereum.org/EIPS/eip-1559>
- EIP-2718 / Typed Transaction Envelope: <https://eips.ethereum.org/EIPS/eip-2718>
- EIP-2930 / Access List Transactions: <https://eips.ethereum.org/EIPS/eip-2930>
- EIP-4844 / Blob Transactions: <https://eips.ethereum.org/EIPS/eip-4844>
- EIP-7702 / Set Code for EOAs: <https://eips.ethereum.org/EIPS/eip-7702>
- Ethereum Foundation Dencun Mainnet Announcement: <https://blog.ethereum.org/2024/02/27/dencun-mainnet-announcement>
- Ethereum Foundation Pectra Mainnet Announcement: <https://blog.ethereum.org/2025/04/23/pectra-mainnet>
- Geth Documentation: <https://geth.ethereum.org/docs/>

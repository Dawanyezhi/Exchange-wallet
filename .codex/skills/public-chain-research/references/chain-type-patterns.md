# 链类型钱包设计模式

## Account 型

典型：ETH、BSC、Polygon、TRON、部分智能合约链。

核心特征：

- 地址有账户余额。
- 交易通常有 Nonce 或类似防重放字段。
- 充值解析通常依赖交易、receipt、event log、internal tx 或链自定义 trace。
- 提现并发重点是 `chain + from_address` 的 Nonce 管理。

设计要点：

- Nonce 必须落库并用事务锁或唯一键保护。
- Token 充值不能信 symbol，必须信合约地址和事件签名。
- 回滚时恢复余额、充值、提现、系统交易和区块高度。
- 费用上限和签后反解析必须覆盖 chain id、to、value、data、gas。

## UTXO 型

典型：BTC、LTC、DOGE、BCH。

核心特征：

- 没有账户余额，余额是 UTXO 集合。
- 每笔提现/归集消耗一个或多个 `txid + vout`。
- 没有 Nonce，防双花依赖 UTXO 唯一消费。
- 原生节点通常不提供任意地址历史查询，需要自建索引或外部索引器。

设计要点：

- 必须有 UTXO 表和状态机：Available、Reserved、PendingSpend、Spent、Reverted、DustFrozen。
- 选币必须在数据库事务中锁定 UTXO。
- 找零地址必须归属本系统。
- 回滚时除了删充值和恢复余额，还要恢复被回滚交易花费的 UTXO。
- 支持 RBF/CPFP 时要明确替换策略和业务输出不变约束。

## Tag/Memo 型

典型：XRP、XLM、ATOM、EOS、HBAR、部分 Cosmos 链。

核心特征：

- 交易所常用一个或少数充值地址，加 memo/tag 区分用户。
- 漏填 memo/tag 会造成自动入账失败。
- memo/tag 本身也是充值归属的一部分。

设计要点：

- 地址和 memo/tag 组合必须唯一映射用户。
- 入账唯一键必须覆盖交易 hash、序号和 memo/tag。
- API、前端和风控必须强提示 memo/tag。
- 提供漏填 memo/tag 的人工找回流程。
- 对账不能只按地址，要按 tag 归属和链上交易核对。

## 多资产合约型

典型：ERC20、TRC20、SPL Token。

核心特征：

- 主币转账和 Token 转账解析路径不同。
- Token 精度、合约地址、事件模型决定入账。
- 可能有转账税、rebase、黑名单、暂停、代理升级等风险。

设计要点：

- Token 白名单以合约地址为准。
- 精度必须从可信配置或链上读取后冻结审计。
- 充值金额以链上实际事件或余额变化为准。
- 异常 Token 要通过 FeatureGate 标记，不要用通用 ERC20 假设覆盖。

## L2/桥接链

典型：Optimism、Arbitrum、zkSync、Starknet、各类桥资产链。

核心特征：

- L2 本地确认和 L1 最终性不是一回事。
- 可能存在 sequencer 停机、强制退出、挑战期或证明延迟。
- 跨链桥是额外信任边界。

设计要点：

- 充值确认数不能只看 L2 出块速度，要结合 L1 finality 和链的安全模型。
- 出金到 L1 的业务状态要覆盖挑战期/证明期。
- 桥资产要区分原生资产、官方桥资产和第三方桥资产。
- 节点和浏览器异常时需要暂停策略。

# 架构设计：irwallet / ethfork 分层模式

## 来源背景

本项目的架构来源于生产级交易所托管钱包系统的分层设计：

```
irwallet（跨链通用层）
    └── ethfork（EVM 兼容链实现层）
           ├── eth-wallet（以太坊主网）
           ├── bsc-wallet（BNB Chain）
           ├── polygon-wallet（Polygon）
           └── metis-wallet（Metis L2）
```

## 层次说明

### irwallet 层 → `internal/`

跨链共享的基础库，所有链实现都依赖它，任何链相关的逻辑都不能放在这里。

| 包 | 职责 |
|---|---|
| `internal/bigint` | 精确金额计算（禁止 float64）；支持 DB `Scan`/`Value` 接口 |
| `internal/coinset` | 链类型枚举（Account/UTXO/Tag）、FeatureGate 特性开关 |
| `internal/alarm` | 告警接口（Lark/PagerDuty/Email），与业务逻辑解耦 |

### ethfork 层 → 各模块 `demo/`

EVM 兼容链的具体实现。由于 ETH/BSC/Polygon 等链接口基本一致，一套代码可支持所有 EVM 链。
链的差异通过 `internal/coinset.ChainConfig`（确认数、FeatureGate）参数化。

| 模块 | 对应 ethfork 组件 |
|---|---|
| `01-key-management` | `ksrv`（签名服务）+ BIP32 推导 |
| `02-block-sync` | `miner`（区块扫描）+ `walletdb`（存储） |
| `03-deposit` | `collector/filter`（充值过滤） |
| `04-withdrawal` | `txbuildn`（交易构建）+ `txsrv`（提现服务） |
| `05-sweep` | `collector`（归集流水线） |
| `06-reconciliation` | 对账模块 |
| `07-mpc-key-management` | MPC 阈值签名方案 |

## 关键设计决策

### 1. FeatureGate 参数化

```go
// ETH 主网：支持 EIP1559 + 内部交易追踪
ETH = ChainConfig{Confirms: 12, Features: EIP1559 | InternalTx}

// BSC：不支持 EIP1559，需要 InternalTx
BSC = ChainConfig{Confirms: 20, Features: InternalTx}

// Metis L2：1个确认足够
Metis = ChainConfig{Confirms: 1, Features: Layer2}
```

同一套 `ethfork` 代码，通过 FeatureGate 适配不同链的特性差异。

### 2. 接口隔离

```
RPCClient     → 可 mock，便于测试
Repository    → 可 mock，无需真实 MySQL
Alarm         → 可 mock，避免测试时真实告警
Signer        → 本地（LocalSigner）/ 远程（walletmanager gRPC）
```

### 3. 金额精度

- 所有金额使用 `*big.Int`（最小计量单位，如 wei）
- 禁止 `float64` 参与金额计算（精度损失）
- 对外展示时再转换为可读格式：`bigint.Readable(amount, decimals)`

### 4. 重组安全

- SafeHeight = Back - confirms（Front 指针）
- 归集前检查 `lastDepositHeight <= SafeHeight`
- 重组时 7 步原子 DB 回滚（MySQL 事务内执行）

## 扩展新链

添加新的 EVM 兼容链只需：

1. 在 `internal/coinset/chain.go` 添加链配置
2. 配置 RPC 端点和确认数
3. 配置 FeatureGate（是否支持 EIP1559/内部交易等）
4. 无需修改任何业务逻辑

添加非 EVM 链（BTC/SOL/TRON）需要实现对应的 `irwallet` 接口（RPCClient/Repository），
然后编写对应链的 `txbuildn`（交易构建）模块。

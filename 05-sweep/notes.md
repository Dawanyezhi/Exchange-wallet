# 05 - 归集深度技术笔记

## 四步流水线设计

### Step 1: Retrieve（取单）

```sql
-- 查询余额超过归集阈值的地址
SELECT address, uid, balance
FROM wallet_accounts
WHERE chain = ? AND kind = 0  -- 0=User 用户地址
  AND balance > ?             -- 超过归集阈值
ORDER BY balance DESC         -- 余额最大的优先
LIMIT 150;                    -- 每批最多150个地址
```

**为什么优先归集 Token**：
- Token 归集需要消耗主币（ETH）支付 Gas
- 如果先归集主币，用户地址的 ETH 耗尽后，Token 就无法归集了
- 正确顺序：先查 Token 余额超阈值的地址 → 检查 ETH 是否够用 → 不够就先补费

### Step 2: Filter（过滤）

**SafeHeight 检查**（最重要）：
```go
// 地址最近充值高度 ≤ SafeHeight 才能归集
// 防止归集重组中的充值
func (f *Filter) checkSafeHeight(addr string, safeHeight int64) bool {
    lastDepositHeight := f.repo.GetLastDepositHeight(addr)
    return lastDepositHeight <= safeHeight
}
```

**Pending 检查**：
```go
// 有 Pending 状态的提现时不归集
// 防止 Nonce 冲突（提现和归集共用同一热钱包地址时）
func (f *Filter) checkNoPendingWithdraw(addr string) bool {
    return f.repo.GetPendingWithdrawCount(addr) == 0
}
```

### Step 3: Build（构建）

**Token 归集**：
```go
// ERC20 transfer(hotWallet, amount)
// 全额归集（amount = 全部 Token 余额）
```

**主币归集**：
```go
// ETH transfer，金额 = 余额 - KeepETH
// KeepETH = 保留用于后续 Gas 的最小主币量
// 例：余额 0.1 ETH，KeepETH = 0.001 ETH，归集 0.099 ETH
```

### Step 4: Fee（补费）
考虑点：是否可以先将同一个地址下需要归集的所有币种进行统计，然后按照币种类型和待归集次数计算总的补费
```go
func (p *Pipeline) fee(ctx context.Context, built <-chan *SweepTx) error {
    for tx := range built {
        // 检查用户地址的 ETH 余额是否够支付 Gas
        ethBalance := p.rpc.GetBalance(ctx, tx.From)
        estimatedGas := tx.GasLimit * tx.GasPrice

        if ethBalance < estimatedGas {
            // 补费：从热钱包转 ETH 到用户地址
            feeAmount := calcFeeAmount(tx.GasPrice)
            p.sendFee(ctx, tx.From, feeAmount)
            // 等待补费交易上链后，再发归集交易
        }

        p.broadcast(ctx, tx)
    }
    return nil
}

// 补费金额按 GasPrice 动态计算（生产中的梯度配置）
// GasPrice ≤ 10Gwei  → 补 0.006 ETH（约1200笔ERC20归集）
// GasPrice ≤ 50Gwei  → 补 0.03  ETH
// GasPrice ≤ 200Gwei → 补 0.12  ETH
// GasPrice >  200Gwei → 暂停归集，等 Gas Price 降低
```

## Channel 流水线的优点

相比"一次性查询全部地址"：
1. **背压控制**：`filter` 处理慢时，`retrieve` 会阻塞在 channel 发送上，不会无限堆积内存
2. **早期终止**：某一步失败可以关闭 channel，下游自动退出
3. **可组合**：每步职责单一，可以独立测试

```go
// 背压示例
func (p *Pipeline) retrieve(ctx context.Context) <-chan *Address {
    ch := make(chan *Address, 10) // 缓冲区10，控制背压
    go func() {
        defer close(ch)
        // 每次取150个地址
        for {
            addrs := p.repo.GetSweepCandidates(ctx, 150)
            for _, addr := range addrs {
                select {
                case ch <- addr:
                case <-ctx.Done():
                    return
                }
            }
            if len(addrs) < 150 { break } // 没有更多地址了
        }
    }()
    return ch
}
```

# 05 - 归集知识点层级

| 层级 | 知识点 |
|------|--------|
| **L3** | 为什么优先归集 Token：Token 余额积压后主币不够支付 Gas，补费后才能归集 |
| **L3** | SafeHeight 过滤的必要性：防止归集重组中的充值（重组后充值被撤销，热钱包资金损失）|
| **L2** | 补费逻辑：主币不足时先从热钱包转一笔 ETH，补费金额按 GasPrice 动态计算 |
| **L2** | Channel 流水线的优点：背压控制，避免一次性加载大量地址 |
| **L1** | 归集阈值设计：Token 通常 0 即归集，主币保留最小 KeepETH |

## 四步流水线数据流

```go
func (p *Pipeline) Run(ctx context.Context) error {
    stream   := p.retrieve(ctx)           // chan *Address
    filtered := p.filter(ctx, stream)    // chan *Address（SafeHeight已过）
    built    := p.build(ctx, filtered)   // chan *SweepTx（含Gas估算）
    return     p.fee(ctx, built)         // 补费后广播
}
```

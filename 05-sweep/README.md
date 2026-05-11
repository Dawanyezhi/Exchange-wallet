# 05 - 归集系统

## 四步流水线

```
Retrieve（取单）→ Filter（过滤）→ Build（构建）→ Fee（补费）
```

- **Retrieve**：查询余额超过阈值的地址，优先归集 Token
- **Filter**：SafeHeight 检查 + Pending 检查 + 黑名单 + 去重
- **Build**：构建 ERC20 transfer 或 ETH transfer
- **Fee**：主币不足时，热钱包先补 Gas

## 运行

```bash
# 演示归集流水线（5种场景：正常归集 / SafeHeight过滤 / 低余额跳过 / Gas超限暂停 / 补费后等待）
go run ./05-sweep/demo/

# 单元测试
go test ./05-sweep/demo/ -v
```

## 补费等待机制

当用户地址 ETH 不足以支付 Gas 时，**本轮不发归集交易**：

```
本轮：hot → user 补 ETH（fee tx）→ continue（跳过归集）
下轮：user ETH 充足 → 发归集交易
```

生产中将 feeHash 写入 `pending_fee_tx` 表，下次 sweep 前先调用
`GetTransactionReceipt(feeHash)` 确认补费已上链，再执行归集。

## 生产差距

- Gas 动态调整阈值（≤10Gwei → 补 0.006ETH）在本 demo 中简化为固定值
- 生产中有 UnsafeCollect 开关（允许归集 SafeHeight 以上的充值）
- 生产中 Filter 阶段有 Pending 检查（该地址有 Pending 提现时不归集）

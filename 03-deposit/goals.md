# 03 - 充值防护知识点层级

| 层级 | 知识点 | 自检问题 |
|------|--------|----------|
| **L3** | 为什么 ERC20 充值看 Log 不看 value 字段 | value 字段是 ETH 金额，ERC20 转账 ETH value=0 |
| **L3** | Receipt.Status=1 为什么不够 | "status=1 但 Transfer 事件金额为0，会上账吗？" |
| **L3** | log.Removed=true 的含义 | "为什么重组期间可能出现 Removed=true 的日志？" |
| **L3** | 二次校验的必要性 | "节点可以伪造 Receipt 吗？如何防御？" |
| **L2** | 内部交易 Trace 的 callType 过滤 | 必须是 call，排除 delegatecall/staticcall |
| **L2** | Token 合约地址白名单 vs symbol 字符串匹配 | |
| **L1** | 标准 Transfer 事件签名的具体哈希值 | `0xddf252ad...` |

## 标准 Transfer 事件签名

```
Transfer(address indexed from, address indexed to, uint256 value)
keccak256("Transfer(address,address,uint256)") = 0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef
```

Topics 格式：`[eventSignature, from_padded_32bytes, to_padded_32bytes]`
Data：`amount_padded_32bytes`（uint256，非 indexed）

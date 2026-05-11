# 04 - 提现知识点层级

| 层级 | 知识点 |
|------|--------|
| **L3** | Nonce 管理为什么必须串行化：一旦 Nonce 跳号，后续所有交易全部堵塞 |
| **L3** | EIP-155 的作用：将 chainId 编入签名哈希，防止跨链重放攻击 |
| **L3** | MaxFee 保护：Gas Price 暴涨时防止手续费超出合理范围 |
| **L2** | 签名后为什么要验签：防止签名服务返回错误签名，浪费 Gas 或遭受篡改 |
| **L2** | 交易 Pending 超时的处理策略：加速（提高 Gas Price）或取消（同 Nonce 空交易）|
| **L1** | RBF（Replace-By-Fee）基本原理：用更高 Gas Price 的同 Nonce 交易替换 Pending 交易 |

## EIP-155 签名原理

```
签名数据 = keccak256(RLP(nonce, gasPrice, gasLimit, to, value, data, chainId, 0, 0))
v = chainId * 2 + 35  (or 36，取决于签名的奇偶性)

多链钱包必须：每条链使用正确的 chainId
否则 ETH 链上的提现交易可以在 ETC 链上重放（两链地址相同！）
```

# 00 - 系统架构设计

## 三层职责划分

```
┌─────────────────────────────────────────────────────────┐
│                   walletmanager（密钥管理层）              │
│   职责：BIP32派生 + Scrypt存储 + X25519加密传输            │
│   特点：一旦上线极少重启，私钥暴露时间最小                   │
└────────────────────────┬────────────────────────────────┘
                         │ 加密私钥（X25519 ECDH + AES-256-GCM）
                         ▼
┌─────────────────────────────────────────────────────────┐
│              syncer + wallet（同步与业务层）               │
│   syncer：区块扫描 + parentHash重组检测 + 7步原子回滚       │
│   wallet：充值7层防护 + 提现Nonce管理 + 归集 + 对账         │
└────────────────────────┬────────────────────────────────┘
                         │ eth_getBlock / eth_sendRawTransaction
                         ▼
┌─────────────────────────────────────────────────────────┐
│               blockchain（EVM兼容链节点）                  │
│   ETH / BSC / Polygon / ETC / Metis ...                  │
└─────────────────────────────────────────────────────────┘
```

## 数据流向

### 充值流
```
区块链新块
  → syncer 扫描区块
  → parentHash 校验（检测重组）
    → 重组：7步原子回滚 → 重新扫描
    → 正常：交易过滤
  → 7层防护过滤（Receipt.Status / Transfer事件 / 白名单 / BlockHash / Trace / 分类 / 二次校验）
  → 写入 wallet_inbound（充值记录）
  → 更新 wallet_accounts（用户余额）
  → 通知上游交易所系统
```

### 提现流
```
交易所发起提现请求
  → NonceManager.Next()（串行分配，mutex保护）
  → TxBuilder.Build()（EIP-155签名，含chainId）
  → Signer.Sign()（walletmanager返回签名）
  → 签名验证（防止签名被篡改）
  → eth_sendRawTransaction 广播
  → syncer 扫块时确认，更新 wallet_outbound 状态
```

### 归集流
```
定时任务触发
  → Retrieve：查询余额 > 阈值的地址（SafeHeight以下）
  → Filter：Pending检查 / 黑名单 / 去重
  → Fee：主币不足时，热钱包先补 Gas
  → Build：构建 ERC20 transfer 或 ETH transfer
  → 广播，syncer 后续确认
```

## 运行

```bash
# 查看完整系统端到端演示（7个场景）
make simulate

# 查看各模块独立演示
make demo
```

## 生产差距

本项目聚焦 **EVM 兼容链（Account 型）**，生产系统还需支持：

- **UTXO 型**（BTC/LTC）：归集时 UTXO 选择算法（最优找零）；重组时额外的 revertUTXO 步骤
- **Tag 型**（XRP/XLM）：充值时 Memo 必须校验；一个地址对应所有用户
- **多节点 failover**：本项目单 RPC 节点，生产中需要多节点轮询 + 健康检查
- **Gas Oracle**：本项目固定 Gas Price，生产中动态调整

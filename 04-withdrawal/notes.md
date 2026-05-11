# 04 - 提现深度技术笔记

## Nonce 管理

### 为什么 Nonce 必须串行化

以太坊交易必须严格按 Nonce 顺序上链：
- 节点只接受 `nonce == account.nonce` 的交易（立即广播）
- `nonce > account.nonce` 的交易进入节点的 pending 队列等待
- **一旦某个 Nonce 跳号（如 Nonce=5 的交易超时失败），后续 Nonce=6,7,8 的交易全部堵塞**

```
账户 Nonce = 5（已上链4笔，下一笔应该是5）
发送 Nonce=5 → 上链，账户 Nonce=6
发送 Nonce=6 → 上链，账户 Nonce=7
发送 Nonce=8（跳过了7！）→ 进入 pending 队列
发送 Nonce=9 → 也在 pending 队列等待
...
Nonce=7 永远不会发出 → Nonce=8,9 永远堵塞
```

### Nonce 恢复方案

```go
// 方法1：发一笔同 Nonce 的空交易（高 Gas Price）
func (nm *NonceManager) Recover(ctx context.Context, stuckNonce uint64) error {
    return nm.sendEmptyTx(ctx, stuckNonce, highGasPrice)
}

// 方法2：重新同步（适用于节点重启后）
func (nm *NonceManager) Sync(ctx context.Context) error {
    count, err := nm.rpc.GetTransactionCount(ctx, nm.address, "pending")
    if err != nil {
        return err
    }
    nm.mu.Lock()
    nm.nonce = count
    nm.mu.Unlock()
    return nil
}
```

### 本地缓存 vs RPC 查询

```go
// 本地缓存（生产首选）
func (nm *NonceManager) Next(ctx context.Context) (uint64, error) {
    nm.mu.Lock()
    defer nm.mu.Unlock()
    if nm.nonce == 0 {
        if err := nm.syncLocked(ctx); err != nil {
            return 0, err
        }
    }
    n := nm.nonce
    nm.nonce++
    return n, nil
}

// 优点：无需每次 RPC 调用，高并发下不依赖节点响应时间
// 缺点：节点重启/切换后需要 Sync()
```

---

## EIP-155 防跨链重放

### 背景

2016 年以太坊分叉为 ETH 和 ETC，两链的地址体系相同（同一私钥派生同一地址）。
EIP-155 之前，同一笔签名可以在两条链上都生效（重放攻击）。

### EIP-155 实现

```go
// 签名前的哈希数据包含 chainId
sigHash = keccak256(RLP(
    nonce, gasPrice, gasLimit, to, value, data,
    chainId, 0, 0,  // EIP-155 新增：链ID + 两个零
))

// 签名后的 v 值编码链ID
v = chainId * 2 + 35  // 或 +36，取决于 recovery id

// 验证时
recoveredChainId = (v - 35) / 2
if recoveredChainId != expectedChainId {
    return ErrWrongChain
}
```

**多链钱包必须**：每条链使用正确的 chainId 签名。
ETH(chainId=1)的交易签名不能用于 BSC(chainId=56)。

---

## 签名验证

```go
// 签名后立即验签，确认签名有效才广播
func (b *TxBuilder) VerifySignature(tx *types.Transaction, sender common.Address) error {
    signer := types.NewEIP155Signer(b.chainID)
    recovered, err := types.Sender(signer, tx)
    if err != nil {
        return fmt.Errorf("verify: recover sender: %w", err)
    }
    if recovered != sender {
        return fmt.Errorf("verify: sender mismatch: got %s, want %s", recovered.Hex(), sender.Hex())
    }
    return nil
}
```

**为什么要验签**：
1. 防止签名服务返回错误签名 → 广播失败 + 浪费 Gas
2. 防止签名被中间人篡改（虽然有加密传输，但防御纵深）
3. 发现密钥对配置错误（私钥对应的地址不是热钱包地址）

---

## MaxFee 保护

```go
const MaxGasPriceGwei = 500  // 生产中从配置读取

func (b *TxBuilder) checkMaxFee(gasPrice *big.Int) error {
    maxGasPrice := new(big.Int).Mul(
        big.NewInt(MaxGasPriceGwei),
        big.NewInt(1e9), // 1 Gwei = 1e9 wei
    )
    if gasPrice.Cmp(maxGasPrice) > 0 {
        return fmt.Errorf("gas price %s exceeds max %s", gasPrice, maxGasPrice)
    }
    return nil
}
```

**场景**：链上拥堵时 Gas Price 可能暴涨到数千 Gwei。
如果不设上限，一笔普通转账可能花费数十 ETH 的手续费，
严重超出预期且无法追回。

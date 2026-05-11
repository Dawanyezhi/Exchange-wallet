# 01 - 密钥管理深度技术笔记

## 密钥存储：Scrypt + AES-128-CTR

### 加密流程（兼容以太坊 Web3 Secret Storage 规范）

```
输入：plaintext（私钥），passphrase（密码）

Step 1: 生成随机 Salt（32字节）
Step 2: Scrypt(passphrase, salt, N=262144, r=8, p=1, dklen=32) → DKey（32字节）
Step 3: IV = random(16字节)
Step 4: ciphertext = AES-128-CTR(key=DKey[0:16], iv=IV, plaintext)
Step 5: MAC = SHA3-256(DKey[16:32] || ciphertext)

存储格式（JSON）：
{
  "cipher": "aes-128-ctr",
  "ciphertext": "<hex>",
  "cipherparams": {"iv": "<hex>"},
  "kdf": "scrypt",
  "kdfparams": {"n": 262144, "r": 8, "p": 1, "dklen": 32, "salt": "<hex>"},
  "mac": "<hex>"
}
```

### MAC 的意义
类似 AEAD（认证加密），防止密文被篡改：
- 解密前先验 MAC（`SHA3-256(DKey[16:32] || ciphertext)` 与存储的 mac 比对）
- 验证失败直接返回错误，不进行解密（防止 padding oracle 攻击等）
- 为什么不直接用 AES-GCM？兼容 Geth/MetaMask 等工具导出的 Keystore 文件格式

---

## 密钥传输：X25519 ECDH + AES-256-GCM

### 场景
walletmanager 需要将派生出的子私钥发送给钱包服务（隔离网络，通过加密 HTTP）。

### 协议流程
```
前向安全（每次传输生成临时密钥对）：

1. 接收方（钱包服务）公开其长期公钥 RecipientPub（X25519）
2. 发送方（walletmanager）生成临时密钥对 (EphemeralPub, EphemeralPriv)
3. 共享密钥：SharedSecret = X25519(EphemeralPriv, RecipientPub)
4. 派生加密密钥：encKey = HKDF-SHA256(SharedSecret, "exchange-wallet-transport")
5. 加密：ciphertext = AES-256-GCM(encKey, nonce, plaintext)
6. 发送：(EphemeralPub, nonce, ciphertext)

接收方解密：
1. SharedSecret = X25519(RecipientPriv, EphemeralPub)
2. encKey = HKDF-SHA256(SharedSecret, ...)
3. plaintext = AES-256-GCM-Decrypt(encKey, nonce, ciphertext)
```

### 为什么使用临时密钥对（前向安全）
- 即使接收方的长期私钥 RecipientPriv 泄漏，攻击者也无法解密历史传输记录
- 因为历史记录中的 EphemeralPriv 已丢弃，无法重算 SharedSecret
- 这是 TLS 1.3 / Signal 协议的核心安全属性

---

## 内存安全

### 四步清除法
```go
func Clear(b []byte) {
    for i := range b { b[i] = 0x00 }  // Step 1: 全零
    for i := range b { b[i] = 0xFF }  // Step 2: 全一（确保写入不被优化）
    rand.Read(b)                        // Step 3: 随机字节（DRAM残留最终覆盖）
    for i := range b { b[i] = 0x00 }  // Step 4: 再次全零
    runtime.KeepAlive(b)               // 防止 GC 在此之前回收内存
}
```

**为什么需要4步**：
- 针对 DRAM 残留（Cold Boot Attack）：电容状态需要多次写入才能稳定
- 防止编译器优化：Go 编译器可能判断"清零后变量未使用"而删除循环
- `runtime.KeepAlive` 确保编译器不会在最后一次"使用"之前提前清理

### XOR 掩码
私钥在内存中**从不以明文形式存放**：

```go
type maskedKey struct {
    masked []byte  // key XOR mask
    mask   []byte  // 随机掩码
}

// 存储时：masked[i] = key[i] ^ mask[i]
// 使用时：key[i] = masked[i] ^ mask[i]  (XOR 的对合性)
// 清除时：先 Clear(masked)，再 Clear(mask)
```

**目的**：
- 使内存扫描工具（如 `/proc/mem` 读取）无法直接找到私钥模式
- 将私钥的"暴露时间窗口"缩小到使用的瞬间

---

## BIP32 派生

### 标准路径结构
```
m / purpose' / coin_type' / account' / change / index

ETH 主地址：m/44'/60'/0'/0/0
BSC 主地址：m/44'/60'/0'/0/0（BSC 复用 ETH 的 coin_type）

内部交易所地址（充值地址）：
  用户0：m/44'/60'/0'/0/0
  用户1：m/44'/60'/0'/0/1
  用户2：m/44'/60'/0'/0/2
```

### 硬化派生（index ≥ 2^31）
- `m/44'/60'` 中的 `'` 表示硬化派生（Hardened Derivation）
- 硬化派生：child_key = HMAC-SHA512(key=parent_privkey, data=0x00 || parent_privkey || index)
- 非硬化派生：child_key = HMAC-SHA512(key=chain_code, data=parent_pubkey || index)

**安全差异**：
- 硬化：即使泄漏了子私钥，攻击者也**无法**推导兄弟密钥或父密钥
- 非硬化：如果泄漏了`(链码 + 任意子私钥)`，可以推导出同层级的所有兄弟密钥

**实践建议**：账户层以上用硬化派生，叶子地址层（大量地址）用非硬化（性能更好）。

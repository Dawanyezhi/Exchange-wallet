# 07 - MPC 密钥管理深度技术笔记

## 从传统方案到 MPC 的演进动机

### 传统方案的根本缺陷

```
walletmanager（持有主种子）
    ↓ X25519 加密传输（完整私钥！）
钱包服务（内存中有完整私钥）
    ↓ 使用私钥签名
```

攻击面：
1. walletmanager 服务器被攻破 → 所有地址私钥泄漏
2. 钱包服务内存被 dump → 当前使用的私钥泄漏
3. 传输通道被中间人攻击（虽然有加密，但加密密钥泄漏后历史传输暴露）

### MPC 方案

```
DKG 阶段（一次性）：
  节点A、B、C 共同执行分布式密钥生成
  生成公钥 PK（所有人知道）
  生成私钥份额 sk_A、sk_B、sk_C（各自持有）
  完整私钥 sk 从未在任何节点出现

签名阶段：
  节点A、B、C 中任意2个参与
  通过 MtA 协议共同计算签名
  签名结果与 sk 签名完全一致
  每个参与方无法从协议中推导出完整私钥
```

---

## EVM 链选 CGGMP21（ECDSA secp256k1）

### 为什么 ECDSA 难做门限签名

ECDSA 签名公式：
```
k = random nonce
R = k·G（点乘法，公开）
r = R.x mod n
s = k⁻¹ · (hash + r·x) mod n
```

问题在于 `k⁻¹`（k 的乘法逆）：
- 秘密分享支持加法：`share_A + share_B = k`（Shamir 多项式）
- 但 `k⁻¹ ≠ share_A⁻¹ + share_B⁻¹`，无法直接秘密分享逆运算

### CGGMP21 的解决方案（MtA 协议）

MtA（Multiplication-to-Addition）：将乘法转化为加法问题，用 Paillier 同态加密实现。

```
简化版流程：
  节点A 持有 k_A，节点B 持有 k_B，使得 k_A + k_B = k（加法秘密分享）

  MtA 协议允许双方计算 c_A + c_B = k_A·k_B（乘法结果）
  而不暴露各自的份额

  通过多次 MtA 组合，最终计算出 s = k⁻¹·(hash + r·x)
```

### CGGMP21 相比 GG18 的5大改进

1. **Aux Info 分离**：辅助信息（Paillier密钥等）与密钥份额分离，方便轮换
2. **Presigning（预签名）**：在知道消息之前预计算签名的大部分工作（提升签名速度）
3. **强制 Range Proof**：修复 BitForge 漏洞，攻击者无法通过畸形密文提取私钥
4. **Identifiable Abort（可识别中止）**：某方作恶时可以识别是谁，不只是"签名失败"
5. **UC 安全证明**：Universal Composability 安全证明，可与其他协议组合

### BitForge 漏洞（L4）

2023年 Fireblocks 研究发现，GG18/GG20 实现中缺少 Range Proof 验证。

攻击者可以：
1. 构造畸形的 Paillier 密文（明文超出 Paillier 密文空间）
2. 在多次签名协议中逐渐提取对方的私钥份额
3. 最终恢复完整私钥

修复：CGGMP21 强制要求每次 MtA 协议中验证 Paillier 密文的 Range Proof。

---

## Ed25519 链选 FROST

### 为什么 Schnorr 容易

Schnorr 签名公式：
```
k = random nonce
R = k·G（公开）
e = hash(R || PK || message)
s = k + e·x（关键：纯加法！）
```

秘密分享加法天然可行：
```
s = k + e·x
  = (k_A + k_B) + e·(x_A + x_B)    ← 加法秘密分享
  = (k_A + e·x_A) + (k_B + e·x_B)  ← 每方独立计算
  = s_A + s_B
```

每方计算自己的 `s_i = k_i + e·x_i`，汇总 `s = Σ s_i`，无需复杂的 MtA 协议。

### FROST 协议特点

- 只需 2 轮通信（MPC 中最少的）
- 适合 2-of-n 门限（任意 2 方可签名）
- 主要用于 Ed25519 曲线（Solana、Stellar 等）
- 比 CGGMP21 简单很多（代码量约 1/5）

### Key Refresh（份额轮换）

```
当前份额：sk_A, sk_B, sk_C（对应公钥 PK）

Key Refresh 后：
  生成新份额：sk_A', sk_B', sk_C'
  对应同一公钥 PK（地址不变！）
  旧份额 sk_A, sk_B, sk_C 作废

效果：
  即使攻击者已经偷到了旧的 sk_A，轮换后也无法合成有效签名
  需要重新攻破2个节点才能获取新的有效份额组合
```

Key Refresh 应该定期执行（如每月一次），或在怀疑节点被攻破时立即执行。

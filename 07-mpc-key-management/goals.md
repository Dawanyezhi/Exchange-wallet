# 07 - MPC 知识点层级

| 层级 | 知识点 | 自检问题 |
|------|--------|----------|
| **L3** | 为什么 ECDSA 难做门限签名（k⁻¹ 非线性）| "为什么 Schnorr 容易，ECDSA 难？" |
| **L3** | MPC vs 多签（MultiSig）的本质区别 | "都是多方控制，为什么选 MPC 不选多签？" |
| **L3** | DKG（分布式密钥生成）解决了什么问题 | "为什么完整私钥从未在任何地方出现？" |
| **L2** | CGGMP21 协议的5大改进（相比 GG18）| Aux Info分离、Presigning、Range Proof、Identifiable Abort、UC证明 |
| **L2** | Key Refresh 的作用 | "刷新后攻击者偷到的旧份额还有用吗？" |
| **L1** | FROST 协议适合哪条曲线 | Ed25519（Schnorr签名，线性可分享）|
| **L4** | BitForge 漏洞的根本原因和修复 | 缺少 Range Proof，允许攻击者通过构造畸形的 Paillier 密文提取私钥份额 |

## 自检答案

**为什么 Schnorr 容易，ECDSA 难？**
- Schnorr 签名：`s = k + e·x`（纯加法，线性可秘密分享）
- ECDSA 签名：`s = k⁻¹ · (hash + r·x) mod n`（含 k⁻¹ 乘法逆，非线性）
- 秘密分享天然支持加法（Shamir 基于多项式加法），不支持求逆
- CGGMP21 用 Paillier 同态加密 + MtA（乘法转加法）协议解决这个问题

**MPC vs 多签的本质区别？**
- 多签：需要链上支持（Gas更高）；签名者地址暴露（隐私差）；改签名策略需换地址
- MPC：链上看到的是普通单签交易；在链下完成协作；地址不变可以轮换份额

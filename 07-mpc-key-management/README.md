# 07 - MPC 密钥管理（概述章节）

> 本章概述为什么传统 Keystore 方案存在安全单点，以及如何用 MPC 门限签名消除它。完整的密码学原理推导和 CGGMP21/FROST 实现，见 [MPC 学习项目](https://github.com/yys9517/mpc-threshold-signature)。

## 传统方案的三大缺陷

1. **主种子单点存储**：一个 keystore 文件 + 一个密码，被攻破即完全失陷
2. **完整私钥传输**：walletmanager 把完整私钥通过加密通道发给钱包服务，钱包服务内存中存在完整私钥
3. **签名服务单点**：签名服务（ksrv）虽然隔离，但仍是单点，被攻破即可任意签名

## 演进路径

```
第一阶段（当前）：传统 Keystore
  walletmanager → [加密通道] → 钱包服务（内存中有完整私钥）

第二阶段（MPC v1，软件）：3方 2-of-3 门限签名
  MPC节点A（份额1） ─┐
  MPC节点B（份额2） ─┤ → 门限签名（完整私钥从未出现）
  MPC节点C（份额3） ─┘

第三阶段（MPC v2，TEE）：
  每个 MPC 节点在 AWS Nitro Enclave 内运行
  份额加密存储于 TEE 内存，root 权限也无法读取
```

## 本章 Demo

只实现 Shamir 秘密分享（作为 MPC 的基础构件），其余指向独立的 MPC 项目。

## 与 MPC 项目的关系

完整实现包含：
- Phase 1: Shamir 秘密分享 + Feldman VSS
- Phase 2: Paillier 同态加密
- Phase 3: CGGMP21 协议（EVM ECDSA 门限签名）
- Phase 4: FROST 协议（Solana Ed25519 门限签名）

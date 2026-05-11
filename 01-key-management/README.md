# 01 - 传统密钥管理

本章实现传统 Keystore 方案：BIP32 派生、Scrypt 加密存储、X25519 加密传输、内存安全清除。

## 私钥全生命周期

```
┌─────────────────────────────────────────────────────────────┐
│                    私钥从不离开 keyman 进程                   │
└─────────────────────────────────────────────────────────────┘

1. 生成与存储
   助记词 → BIP39 → 64字节种子 → Scrypt(N=2^18)+AES-128-CTR 加密
   → 写入 keyman_master_seed 表（或 keystore/root.json 文件）
   → 明文种子立即四步清零

2. 服务启动（加载到内存）
   DB/文件 → 读取加密种子 → 输入密码 → Scrypt 解密 → 验证 MAC
   → XOR Mask 保护存入内存（maskedKey = seed XOR randomMask）
   → 明文种子立即清零
   → 从 keyman_derived_addresses 全量加载已派生地址到内存缓存

3. 地址派生（用户注册时）
   Wallet 服务 →(X25519加密)→ Keyman POST /keyman/derive
   → SELECT next_index FROM keyman_derivation_counter FOR UPDATE
   → maskedKey.Reveal → BIP32 硬化派生 m/fnv1a(chain)'/index'
   → 提取公钥+地址 → 清零子密钥
   → INSERT INTO keyman_derived_addresses（存公钥，不存私钥）
   → UPDATE keyman_derivation_counter SET next_index++
   → 返回地址+公钥给 Wallet 服务

4. 签名（提现/归集时）
   Wallet 服务 →(X25519加密)→ Keyman POST /keyman/sign
   → Ed25519 验签 + 时间戳窗口防重放
   → (chain, address) 查内存缓存得 account_index
   → maskedKey.Reveal → BIP32 派生子密钥 → ECDSA 签名 → 清零子密钥
   → INSERT INTO keyman_sign_audit（审计日志）
   → 返回签名（65字节 [r||s||v]）

5. 服务关闭
   SIGTERM → 四步清零内存中所有密钥材料（0x00→0xFF→random→0x00）
   → runtime.KeepAlive 防止 GC 提前回收
```

## 核心组件

| 文件 | 功能 | 关联 |
|------|------|------|
| **bip32.go** | Ed25519 BIP32 硬化派生（HMAC-SHA512） | keyman 派生地址时调用 |
| **passphrase.go** | Scrypt+AES-128-CTR 加密/解密（兼容 Web3 Keystore V3） | keyman 启动时解密种子 |
| **memory_clear.go** | 四步内存清除 + XOR 掩码保护 | keyman 全生命周期使用 |
| **transport.go** | X25519 ECDH + AES-256-GCM 加密传输 | wallet↔keyman 通信加密 |

服务入口：[`cmd/keyman/main.go`](../cmd/keyman/main.go)
数据库表：[`docs/database-schema.sql`](../docs/database-schema.sql)（keyman_master_seed / keyman_derived_addresses / keyman_sign_audit）
API 设计：[`docs/api-design.md`](../docs/api-design.md)（Part 2: Keyman API）
配置文件：[`config/keyman-dev.yaml`](../config/keyman-dev.yaml)

## 四层安全架构

```
存储层    Scrypt(N=2^18, 256MB内存) + AES-128-CTR + MAC 验证
          → 暴力破解一个密码需要 ~2^77 次 Scrypt 运算
          → 兼容 Geth/MetaMask 的 Web3 Keystore V3 格式

传输层    X25519 ECDH + AES-256-GCM，每次请求生成临时密钥对
          → 前向安全：即使长期私钥泄漏，历史传输不可解密
          → 等同 TLS 1.3 / Signal 协议的安全属性

鉴权层    Ed25519 签名 + 时间戳窗口（-1s ~ +60s）
          → 防重放攻击：过期请求直接拒绝

内存层    XOR Mask 保护 + 四步清零 + runtime.KeepAlive
          → 私钥在内存中从不以明文形式存放
          → 使用时临时 XOR 恢复，用完立即清零
          → 防 core dump / /proc/mem 扫描 / Cold Boot Attack
```

## 生产差距

- 本 demo 未实现 HTTP API 层和 Protobuf 签名鉴权（`cmd/keyman` 有完整注释但 stub 实现）
- 生产中 Scrypt N=2^18（占用 256MB 内存），demo 测试时降低到 N=2^12 加速
- 生产中数据库操作使用事务+行锁（`SELECT ... FOR UPDATE`），demo 使用内存 map
- 审计日志在生产中写入独立数据库表，demo 仅打印日志

## 私钥的本质缺陷与 MPC 升级路径

传统 Keystore 方案存在三个根本缺陷：
1. **主种子单点存储**：一个文件/DB记录 + 一个密码，被攻破即完全失陷
2. **完整私钥在内存**：XOR Mask 只是增加攻击成本，不是根本解决
3. **签名服务单点**：签名服务虽然隔离，仍是单点，被攻破可任意签名

完整的 MPC 门限签名方案见 [第07章](../07-mpc-key-management/)，彻底消除这三个单点。
两种方案的详细对比见主 [README.md](../README.md#密钥管理方案对比)。

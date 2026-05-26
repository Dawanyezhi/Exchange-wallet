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
   → Ed25519 签名 proto.Marshal({uid, coin, address}) → addrSig
   → INSERT INTO keyman_derived_addresses（存公钥，不存私钥）
   → UPDATE keyman_derivation_counter SET next_index++
   → 返回地址+公钥+addrSig 给 Wallet 服务（Wallet 服务将 addrSig 存入 DB）
   （后续每次查询该地址时 Wallet 服务用 Keyman 公钥验签，防止 DB 被篡改后充值重定向）

4. 签名（提现/归集时）
   Wallet 服务 构建原始交易 → EIP-155 RLP 编码 → 取哈希
   Wallet 服务 →(X25519+AES-GCM 加密请求)→ Keyman POST /keyman/sign
   → Ed25519 验签 + 时间戳窗口防重放
   → (chain, address) 查内存缓存得 account_index
   → maskedKey.Reveal → BIP32 派生子私钥 → ECDSA 签名哈希 → 清零子私钥
   → INSERT INTO keyman_sign_audit（审计日志）
   → 签名结果（65字节 [r||s||v]）→(X25519+AES-GCM 加密响应)→ 返回 Wallet 服务
   Wallet 服务 解密 → 拼装完整签名交易 → eth_sendRawTransaction 广播

5. 服务关闭
   SIGTERM → 四步清零内存中所有密钥材料（0x00→0xFF→random→0x00）
   → runtime.KeepAlive 防止 GC 提前回收
```

## 核心组件

| 文件 | 功能 | 关联 |
|------|------|------|
| **bip32.go** | Ed25519 BIP32 硬化派生（HMAC-SHA512） | keyman 派生地址时调用 |
| **bip32_secp256k1.go** | secp256k1 BIP32/BIP44 派生（EVM 可用） | ETH/EVM 地址派生与 ECDSA 签名 |
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

## Ed25519 与 secp256k1 派生

本章同时保留了两个派生 demo：

- [`demo/bip32.go`](./demo/bip32.go)：Ed25519 风格的 SLIP-0010 硬化派生，用于展示签名服务内部密钥派生、安全鉴权和教学流程。
- [`demo/bip32_secp256k1.go`](./demo/bip32_secp256k1.go)：标准 secp256k1 BIP32/BIP44 派生，用于生成 ETH/EVM 地址和 ECDSA 交易签名私钥。

### 核心区别

| 对比项 | Ed25519（`bip32.go`） | secp256k1（`bip32_secp256k1.go`） |
|--------|------------------------|-----------------------------------|
| 曲线/算法 | Ed25519 / EdDSA | secp256k1 / ECDSA |
| 主密钥域分离常量 | `ed25519 seed` | `Bitcoin seed`（BIP32 标准常量，不代表只能用于 BTC） |
| 派生规范 | 类 SLIP-0010，只做 hardened 派生 | BIP32，支持 hardened 与 non-hardened |
| demo 路径表达 | 用链名映射 hardened index，例如 `Child("ETH")` | 用 BIP44 路径，例如 `m/44'/60'/0'/0/0` |
| 公钥/地址 | 32 字节 Ed25519 公钥，不能直接生成 EVM 地址 | 可生成 EVM 地址，`crypto.PubkeyToAddress` |
| 交易签名 | 不适合 ETH/EVM 原生交易签名 | ETH/EVM 原生交易签名使用该曲线 |

Ed25519 和 secp256k1 的私钥、公钥、签名格式都不兼容。不能用 Ed25519 私钥去签 ETH 交易，也不能把 secp256k1 地址当作 Ed25519 地址验签。

### 什么情况下使用

使用 Ed25519：

- keyman/wallet 内部请求鉴权，例如请求体签名、地址记录签名、防重放校验。
- 教学场景中演示 hardened 派生、链码、内存清除等概念。
- 链本身采用 Ed25519 的场景，例如部分 Solana、Cosmos SDK 变体或其他 EdDSA 链。具体仍需按目标链的地址和签名规范实现。

使用 secp256k1：

- ETH、EVM 兼容链、BTC 等使用 secp256k1 的链。
- 需要生成标准 BIP44 地址路径时，例如 ETH 常用 `m/44'/60'/0'/0/index`。
- 需要拿到 `*ecdsa.PrivateKey` 或 EVM 地址，用于构造、签名和广播链上交易。

在交易所托管钱包里，常见做法是：内部服务鉴权可以用 Ed25519；链上资产地址和交易签名必须跟随目标链曲线。ETH/EVM 不能用 `bip32.go` 的 Ed25519 结果签链上交易，应使用 `bip32_secp256k1.go`。

### 如何使用 secp256k1 BIP44 派生

```go
seed := MnemonicToSeed(mnemonic, "")

master, err := NewSecp256k1MasterKey(seed)
if err != nil {
    return err
}
defer master.ClearKey()

account, err := master.DerivePath("m/44'/60'/0'/0/0")
if err != nil {
    return err
}
defer account.ClearKey()

address, err := account.EthereumAddress()
if err != nil {
    return err
}

privateKey, err := account.PrivateKey()
if err != nil {
    return err
}

_ = address
_ = privateKey
```

对应测试：

```bash
go test ./01-key-management/demo -run '^TestSecp256k1BIP44DerivationDifferentIndexes$' -count=1 -v
```

也可以通过 Makefile 执行：

```bash
make test-bip44-indexes
```

### 使用规范

- 主种子只保存一次，落盘前必须用 Scrypt + AES 加密；不要把助记词、seed、私钥明文写入日志或数据库。
- 每条链使用自己的派生规范：ETH/EVM 使用 secp256k1 + BIP44；Ed25519 链使用对应链确认过的 SLIP-0010/地址规范。
- ETH/EVM 推荐路径格式为 `m/44'/60'/account'/0/index`，其中 `index` 对应用户地址序号；生产中要用数据库事务和行锁维护 `next_index`，避免并发重复派生。
- 不同用途的密钥要隔离：内部鉴权密钥、地址派生密钥、提现签名密钥不要混用同一条派生路径。
- 子私钥只在签名或导出地址时短暂存在，用完立即调用 `ClearKey()`；返回给外部服务的只能是地址、公钥、签名结果和审计信息。
- 测试里可以打印地址和临时私钥用于验证；生产代码禁止打印私钥、seed、chain code、助记词。
- 不要把本 demo 包装成生产级 HD 钱包库。生产系统还需要补齐地址版本、链参数、交易编码、审计、权限控制、HSM/MPC 或更严格的签名隔离。

## 私钥的本质缺陷与 MPC 升级路径

传统 Keystore 方案存在三个根本缺陷：
1. **主种子单点存储**：一个文件/DB记录 + 一个密码，被攻破即完全失陷
2. **完整私钥在内存**：XOR Mask 只是增加攻击成本，不是根本解决
3. **签名服务单点**：签名服务虽然隔离，仍是单点，被攻破可任意签名

完整的 MPC 门限签名方案见 [第07章](../07-mpc-key-management/)，彻底消除这三个单点。
两种方案的详细对比见主 [README.md](../README.md#密钥管理方案对比)。

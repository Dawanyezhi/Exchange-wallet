# API 接口设计

## 概述

系统包含两个独立服务，各自提供独立的 API：

| 服务 | 监听地址 | 调用方 | 传输加密 |
|------|----------|--------|----------|
| **Wallet 钱包服务** | 0.0.0.0:8080 | 交易所业务系统 | HTTPS |
| **Keyman 密钥管理服务** | 127.0.0.1:9000（仅内网） | Wallet 服务 | X25519 ECDH + AES-256-GCM |

金额字段统一使用字符串（big.Int），禁止 float64。

---

# Part 1: Wallet 钱包服务 API

对外提供 RESTful HTTP API，供交易所业务系统调用。

---

## 通用响应格式

```json
{
  "code": 0,        // 0=成功，非0=错误
  "msg": "ok",      // 错误信息
  "data": {}        // 响应数据
}
```

---

## 接口列表

### GET /status
**服务健康检查**

```
Response 200:
{
  "code": 0,
  "data": {
    "status": "ok",
    "chain": "ETH",
    "back_height": 19000000,
    "front_height": 18999988,
    "confirms": 12
  }
}
```

---

### POST /withdraw
**发起提现请求**

```
Request:
{
  "qid": "123456",            // 交易所提现单ID（幂等键，同一qid只处理一次）
  "uid": 1001,                // 用户ID
  "chain": "ETH",
  "symbol": "USDT",
  "to": "0x1234...",          // 目标地址
  "amount": "100000000"       // 金额（链上最小单位字符串）
}

Response 200:
{
  "code": 0,
  "data": {
    "id": 789,                // 提现记录ID
    "status": "pending"       // 已提交到队列
  }
}

Response 400 (重复请求):
{
  "code": 1001,
  "msg": "duplicate withdrawal request: qid=123456"
}
```

---

### GET /deposit/:address
**查询地址充值记录**

```
Query params:
  chain: ETH
  symbol: USDT（可选）
  limit: 20（默认）
  offset: 0

Response 200:
{
  "code": 0,
  "data": {
    "total": 5,
    "list": [
      {
        "id": 123,
        "chain": "ETH",
        "symbol": "USDT",
        "hash": "0xabc...",
        "from": "0x9999...",
        "to": "0x1234...",
        "amount": "100000000",    // 链上单位
        "amount_readable": "100.000000",  // 人类可读
        "height": 18999900,
        "status": "success",
        "btime": "2024-01-01T12:00:00Z"
      }
    ]
  }
}
```

---

### GET /balance/:address
**查询地址余额**

```
Query params:
  chain: ETH
  symbol: USDT（可选，不填返回所有代币）

Response 200:
{
  "code": 0,
  "data": {
    "address": "0x1234...",
    "chain": "ETH",
    "balances": [
      {
        "symbol": "ETH",
        "balance": "1000000000000000000",
        "balance_readable": "1.000000000000000000"
      },
      {
        "symbol": "USDT",
        "balance": "100000000",
        "balance_readable": "100.000000"
      }
    ]
  }
}
```

---

### POST /sweep/trigger
**手动触发归集（需权限）**

```
Header:
  X-Admin-Token: <admin_token>

Request:
{
  "chain": "ETH",
  "symbol": "USDT",          // 可选，不填则归集所有代币
  "address": "0x1234..."     // 可选，不填则归集所有满足条件的地址
}

Response 200:
{
  "code": 0,
  "data": {
    "triggered": 5,           // 触发归集的地址数量
    "sweep_ids": [1001, 1002, 1003, 1004, 1005]
  }
}
```

---

# Part 2: Keyman 密钥管理服务 API

仅监听 127.0.0.1:9000（内网），Wallet 服务通过加密通道调用。
所有请求/响应的 body 通过 X25519 ECDH + AES-256-GCM 加密传输。

**鉴权方式**：Ed25519 签名 + 时间戳窗口（-1s ~ +60s），防重放。

```
请求头（加密前的明文结构）：
{
  "timestamp": 1704067200,           // Unix 秒级时间戳
  "signature": "<Ed25519签名>",      // Sign(timestamp + request_body)
  "caller": "wallet-service"         // 调用方标识
}
```

---

### GET /keyman/status
**服务健康检查**

```
Response 200:
{
  "code": 0,
  "data": {
    "status": "ok",
    "seed_loaded": true,
    "supported_chains": ["ETH", "BSC", "POLYGON"],
    "total_derived": 10235,
    "uptime_seconds": 86400
  }
}
```

---

### POST /keyman/derive
**派生新地址**

Wallet 服务在用户注册时调用，获取充值地址。Keyman 内部完成：
1. 从 `keyman_derivation_counter` 获取并自增 next_index（SELECT ... FOR UPDATE）
2. BIP32 硬化派生：`m / fnv1a(chain)' / account_index'`
3. 将派生记录写入 `keyman_derived_addresses`（存公钥，不存私钥）
4. 返回地址和公钥

```
Request:
{
  "chain": "ETH",
  "kind": 0,              // 0=User 1=Hot 2=Cold
  "count": 1              // 批量派生数量（默认1，最大100）
}

Response 200:
{
  "code": 0,
  "data": {
    "addresses": [
      {
        "chain": "ETH",
        "address": "0x1234...",
        "pubkey": "0x04abc...",           // 未压缩公钥（65字节 hex）
        "derivation_path": "m/2149580400'/0'",
        "account_index": 0
      }
    ]
  }
}
```

---

### POST /keyman/sign
**签名请求**

Wallet 服务在提现/归集时调用。Keyman 内部完成：
1. 通过 (chain, address) 查 `keyman_derived_addresses` 获取 account_index
2. 从内存中的 XOR Masked rootKey 恢复主密钥
3. BIP32 派生到目标子密钥
4. 对 tx_hash 执行 secp256k1 ECDSA 签名
5. 四步清零临时密钥材料
6. 写入 `keyman_sign_audit` 审计日志
7. 返回签名结果（通过 X25519 加密通道）

```
Request:
{
  "chain": "ETH",
  "address": "0x1234...",          // 签名地址
  "tx_hash": "0xabcdef..."        // 待签名的 32 字节哈希（hex）
}

Response 200:
{
  "code": 0,
  "data": {
    "signature": "0x...",          // 65字节签名 [r(32) || s(32) || v(1)]（hex）
    "address": "0x1234..."
  }
}

Response 403 (频率限制/黑名单):
{
  "code": 4003,
  "msg": "sign denied: rate limit exceeded for address 0x1234..."
}
```

---

### POST /keyman/verify
**验证签名（不涉及私钥，用公钥验证）**

```
Request:
{
  "chain": "ETH",
  "address": "0x1234...",
  "tx_hash": "0xabcdef...",
  "signature": "0x..."
}

Response 200:
{
  "code": 0,
  "data": {
    "valid": true
  }
}
```

---

### GET /keyman/audit
**查询签名审计日志（仅管理员）**

```
Query params:
  chain: ETH
  address: 0x1234...（可选）
  start_time: 2024-01-01T00:00:00Z
  end_time: 2024-01-02T00:00:00Z
  limit: 50

Response 200:
{
  "code": 0,
  "data": {
    "total": 128,
    "list": [
      {
        "chain": "ETH",
        "address": "0x1234...",
        "tx_hash": "0xabc...",
        "caller_service": "wallet-withdrawal",
        "request_ip": "10.0.1.5",
        "result": "success",
        "ctime": "2024-01-01T12:00:00Z"
      }
    ]
  }
}
```

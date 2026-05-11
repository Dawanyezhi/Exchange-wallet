# 部署指南

## Docker Compose 部署

### 文件结构

```
exchange-wallet/
├── docker-compose.yml
├── config/
│   ├── wallet-dev.yaml
│   └── keyman-dev.yaml
└── ...
```

### docker-compose.yml

```yaml
version: '3.8'

services:
  mysql:
    image: mysql:8.0
    environment:
      MYSQL_ROOT_PASSWORD: rootpassword
      MYSQL_DATABASE: exchange_wallet
      MYSQL_USER: wallet
      MYSQL_PASSWORD: walletpassword
    ports:
      - "3306:3306"
    volumes:
      - mysql_data:/var/lib/mysql
      - ./docs/database-schema.sql:/docker-entrypoint-initdb.d/schema.sql
    healthcheck:
      test: ["CMD", "mysqladmin", "ping", "-h", "localhost"]
      interval: 10s
      timeout: 5s
      retries: 5

  wallet:
    build: .
    command: ["/app/wallet", "-config", "/app/config/wallet-dev.yaml"]
    environment:
      - DB_DSN=wallet:walletpassword@tcp(mysql:3306)/exchange_wallet?parseTime=true
      - LARK_WEBHOOK=${LARK_WEBHOOK}
    ports:
      - "8080:8080"
    depends_on:
      mysql:
        condition: service_healthy
    volumes:
      - ./config:/app/config:ro

  keyman:
    build: .
    command: ["/app/keyman", "-config", "/app/config/keyman-dev.yaml"]
    ports:
      - "8081:8081"
    volumes:
      - ./keystore:/app/keystore:ro
      - ./config:/app/config:ro

volumes:
  mysql_data:
```

---

## 环境变量说明

| 变量 | 必填 | 说明 |
|------|------|------|
| `DB_DSN` | 是 | MySQL 连接字符串 |
| `LARK_WEBHOOK` | 否 | Lark 告警 Webhook URL |
| `KEYMAN_PASSPHRASE` | 是（keyman）| Keystore 解密密码 |
| `ETH_RPC_KEY` | 是 | 以太坊节点 API Key |

---

## 最小生产部署清单

### 本项目已实现

- [x] 区块同步 + 重组检测 + 7步原子回滚
- [x] 7层假充值防护
- [x] Nonce 串行管理 + EIP-155 签名
- [x] 归集四步流水线
- [x] 余额对账
- [x] Keystore 密钥管理 + X25519 传输

### 生产环境还需要补充

- [ ] **多节点 failover**：本项目单 RPC 节点，生产中需要 2-3 个节点轮询 + 健康检查
- [ ] **Bloom Filter**：生产中地址匹配用 Bloom Filter（O(1)），本项目用内存 map
- [ ] **gRPC / Protobuf**：walletmanager 和钱包服务之间的通信，本项目用 HTTP
- [ ] **Ed25519 签名防重放**：密钥分发请求需要时间戳签名，本项目未实现
- [ ] **完整 Nonce 恢复**：卡 Nonce 时自动发空交易恢复，本项目只有接口定义
- [ ] **RBF 加速**：提现超时后自动提高 Gas Price，本项目只有接口定义
- [ ] **MPC 门限签名**：替代单节点 Keystore 方案，见第07章和 MPC 项目
- [ ] **监控大盘**：Prometheus + Grafana，本项目只有日志
- [ ] **UTXO/Tag 型链**：本项目只支持 EVM Account 型链
- [ ] **告警去重**：45分钟内同内容只发一次，本项目简化为内存记录（重启后重置）

---

## 快速启动

```bash
# 1. 克隆项目
git clone https://github.com/yys9517/exchange-wallet
cd exchange-wallet

# 2. 运行所有测试（不依赖 MySQL，使用内存 mock）
make test

# 3. 运行各章节 demo
make demo

# 4. Docker Compose 启动（需要 Docker）
docker-compose up -d mysql
sleep 10  # 等待 MySQL 就绪
docker-compose up -d wallet keyman
```

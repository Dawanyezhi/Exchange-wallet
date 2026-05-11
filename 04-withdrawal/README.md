# 04 - 提现流程

## 核心实现

- **nonce_manager.go**：Nonce 串行管理（mutex 保护，防止 Nonce 冲突）
- **tx_builder.go**：EIP-155 签名交易构建 + 签名验证
- **signer.go**：签名接口（支持本地和远程签名）

## 生产差距

- 本 demo 远程签名（ksrv）使用本地私钥模拟，生产中是独立的签名服务（隔离网络）
- MaxFee 检查在本 demo 中硬编码，生产中从配置动态读取
- 生产中有 RBF（Replace-By-Fee）加速机制，本 demo 提供接口定义但不实现
- 生产中 Nonce 卡住恢复（发一笔同 Nonce 的空交易）本 demo 只有接口

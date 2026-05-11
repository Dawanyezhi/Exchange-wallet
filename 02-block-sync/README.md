# 02 - 区块同步 + 重组处理

> 区块链的重组（Reorg）是交易所钱包系统最危险的场景之一。本章实现了完整的重组检测 + 原子性 7 步回滚，并包含可模拟重组的测试套件。

## 核心实现

- **syncer.go**：主同步循环，轮询最新块，调度重组检测和充值处理
- **reorg.go**：parentHash 重组检测 + 7步原子性数据库回滚
- **inbound.go**：充值记录写入与确认
- **repository/block.go**：区块头 CRUD（滑动窗口）
- **repository/balance.go**：余额管理（带回滚历史）
- **mock/mock_rpc.go**：可制造重组的模拟 RPC 节点

## 生产差距

- 本 demo 只实现 EVM Account 型链，未实现 UTXO 的 `revertUTXO` 步骤
- 生产中 rollback 超时为 120s，支持配置；本 demo 固定超时
- 深度重组（Front==Back）会触发 Lark 告警 + 人工介入；本 demo 记录错误并停止
- 生产系统使用 Bloom Filter 加速地址匹配（O(1) 查找），本 demo 使用内存 map

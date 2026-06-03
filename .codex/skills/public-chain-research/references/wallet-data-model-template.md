# 钱包数据模型模板

## 通用表

建议所有链至少有：

- 区块头表：`chain,height,hash,parent,btime`，用于重组检测。
- 高度表：`chain,front,back`，用于安全高度滑动窗口。
- 地址表：`chain,address,uid,kind,symbol,pubkey,derivation_path,status`。
- 充值表：`chain,symbol,height,hash,from,to,value,fee,status,uid,btime`。
- 提现表：`chain,symbol,height,hash,from,to,value,fee,status,qid,uid,rawtx`。
- 系统交易表：归集、补费、找零整理、热冷转账。
- 余额表和余额流水表：余额快照必须可由流水重建。
- 签名审计表：记录请求方、地址、交易摘要、结果和拒绝原因。

字段长度不要沿用 EVM 默认值。地址、hash、memo、signature、rawtx 都要按目标链重新确认。

## Account 型扩展

需要：

- Nonce 表：`chain,from_address,next_nonce,mtime`。
- 提现交易唯一键：`chain,from_address,nonce`。
- Token 交易表或 log 表：保存 `contract,log_index,event_signature`。
- internal tx/trace 表：如果充值依赖内部交易。

关键索引：

- `chain,height` 用于回滚。
- `chain,hash,to` 或 `chain,hash,log_index` 用于防重复充值。
- `chain,status` 用于扫描待处理提现。

## UTXO 型扩展

需要 UTXO 状态表：

```text
chain
txid
vout
address
uid
address_kind
symbol
amount
script_pubkey
script_type
block_height
block_hash
status
spend_txid
spend_height
reserved_by
reserved_at
```

唯一键：

- `chain,txid,vout`

关键索引：

- `chain,address,status`
- `chain,status,amount`
- `chain,spend_txid`
- `chain,block_height`

推荐状态机：

```text
Available -> Reserved
Reserved -> PendingSpend
PendingSpend -> Spent
Reserved/PendingSpend -> Available
Available/PendingSpend/Spent -> Reverted
Spent -> Available  // 花费交易所在区块被回滚
```

还应有：

- 交易输入表：保存当前 tx 的 vin 和 previous outpoint。
- 交易输出表：保存所有输出或至少保存本系统输出。
- 选币绑定表：把业务单和输入 UTXO 固定，防止并发复用。
- rawtx 表：保存构建、签名、广播、替换的原始交易和费率。

## Tag/Memo 型扩展

需要：

- memo/tag 映射表：`chain,address,memo_tag,uid,status`。
- 漏填 memo 处理表：记录待人工认领的充值。
- 入账唯一键包含 memo/tag 和交易内序号。

关键风险：

- 不能只按地址入账。
- memo/tag 类型和编码必须严格校验。
- 用户提现到带 memo 链时，也要保存目标 memo/tag 并签前校验。

## 回滚设计

所有链都要能按高度回滚：

1. 恢复余额。
2. 删除或标记充值记录。
3. 提现从 Success 回 Pending/OnChainPending。
4. 系统交易回 Pending。
5. 删除区块头。
6. 更新 Front/Back。
7. UTXO 链额外恢复 UTXO：删除被回滚区块新产生的本系统 UTXO，恢复被该区块交易花费的旧 UTXO。

回滚必须在数据库事务内完成，不能出现半回滚状态。

## 金额字段

金额统一用链上最小单位的整数字符串或高精度整数序列化：

- 不使用 `float64`。
- 不用展示精度参与记账。
- Token decimals 只用于展示和 API 转换，不用于内部金额计算。

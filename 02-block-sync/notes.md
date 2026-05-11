# 02 - 区块同步深度技术笔记

## 区块链 Reorg 本质

两个矿工（或验证者）同时产生不同的块，网络形成临时分叉，最终选择"更长链"（PoW：最大累计难度；PoS：Finality）。

**钱包视角**：之前认为已确认的交易可能被撤销，充值记录需要删除，提现可能从 Success 退回 Pending。

**真实案例**：
- Polygon 在高度 25280775 附近发生深度分叉，持续约 1 小时
- ETC 2020年8月遭受三次51%攻击，最深重组 3693 个块
- BSC 2022年发生多次短暂分叉（通常 1-2 块深度）

---

## parentHash 检测算法

```
扫描到新块 newHeader:
  获取本地最新块 localHead

  if localHead == nil:
    首次同步，直接接受
  elif newHeader.Height == localHead.Height + 1:
    if newHeader.ParentHash == localHead.Hash:
      正常连接，处理该块
    else:
      parentHash 不匹配！触发回滚
      回滚深度 = 找到公共祖先的距离
  elif newHeader.Height <= localHead.Height:
    旧块（已处理），忽略
  else:
    跳块（newHeader.Height > localHead.Height + 1）
    等待中间块，或直接跳过补扫
```

**为什么单检查高度不够**：
分叉时，新分叉链的块高度比本地高1（正常），但 parentHash 不同。
如果只检查高度，会把分叉块当成正常块接受，导致充值记录错误。

---

## 7步数据库回滚事务

```
在一个数据库事务（BEGIN ... COMMIT）中按顺序执行：

Step 1: revertBalance
  - 查询 wallet_balance_log WHERE chain=? AND height=?
  - 对每条记录：UPDATE wallet_accounts SET balance = balance - delta WHERE address=?
  - 原理：balance_log.delta 是正数（充值增加），回滚时减去

Step 2: revertInboundTx
  - DELETE FROM wallet_inbound WHERE chain=? AND height=?
  - 删除该块产生的所有充值记录

Step 3: revertOutboundTx
  - UPDATE wallet_outbound SET status=1（Pending）, height=0
    WHERE chain=? AND height=? AND status=2（Success）
  - 将已确认的提现状态退回 Pending，等待重新确认

Step 4: revertSystemTx
  - 类似 Step 3，处理归集/补费等系统交易
  - UPDATE wallet_system_txs SET status='Pending' WHERE chain=? AND height=?

Step 5: revertHeader
  - DELETE FROM wallet_blocks WHERE chain=? AND height=?
  - 删除该高度的区块头记录

Step 6: revertHeight
  - UPDATE wallet_heights SET back=back-1 WHERE chain=?
  - Front（SafeHeight）不变，Back 减1

COMMIT（以上全部成功才提交，任何失败全部回滚）
```

**为什么顺序不能颠倒（关键）**：

1. Step 1（余额）必须在 Step 2（充值）之前：
   - 余额是从充值记录聚合来的
   - 删充值记录前必须先把余额改回去，否则余额变成孤立数据

2. Step 3（提现）必须在 Step 5（区块头）之前：
   - 提现记录的 height 字段引用区块头
   - 提前删区块头会导致外键约束失败（如果有外键）或数据孤立

3. Step 5（区块头）必须在 Step 6（高度）之前：
   - 先改高度后删块头，会导致短暂的高度和块头不一致窗口

---

## Front/Back 滑动窗口

```
Block: 988  989  990  991  992  993  994  995  996  997  998  999 1000
       |---------- 窗口内（保留区块头用于重组检测）----------|
       ↑                                                      ↑
     Front=988                                            Back=1000
    (SafeHeight)

确认数 = 12
Back  = 1000（链上最新块）
Front = 1000 - 12 = 988（安全高度）

规则：
- Front 以下的充值：确认完毕，可上账、可归集
- Front ~ Back 之间：等待确认，不可归集
- Back 以上：尚未扫描
```

**正常同步时的变化**：
```
新块 1001 到来 → Back = 1001 → Front = 1001 - 12 = 989
新块 1002 到来 → Back = 1002 → Front = 1002 - 12 = 990
（Front 和 Back 同步向前推进）
```

**重组时的变化**：
```
已扫描到 1000，Front=988，Back=1000
发现重组，需要回滚 2 块：
  回滚 1000 → Back = 999
  回滚 999  → Back = 998
Front 不变（仍为 988），等待正确的块填充 999、1000 位置
```

**深度重组（Front == Back）**：
```
只剩 Front=988，Back=988
无法继续自动回滚（整个窗口已空）
触发告警，等待人工介入
```

---

## UTXO 链的额外步骤（L4）

Account 型链（ETH）的余额是状态（`mapping address => balance`），回滚只需更新余额。

UTXO 型链（BTC）的余额是 UTXO 集合（`set of unspent outputs`），回滚时需要：

```
Step 7: revertUTXO（仅 UTXO 链）
  - 找到该块中所有交易的输入（已花费的 UTXO）
  - UPDATE wallet_utxos SET status='Available' WHERE txid=? AND vout=?
    WHERE status='Spent' AND spent_height=?

原理：该块的交易花费了一些 UTXO，重组后这些交易不存在了，
     被花费的 UTXO 应该恢复为可用状态
```

如果漏做这步，归集时看到 UTXO 的状态是 Spent，但实际上还可以使用，
会导致链上余额和链下账本不一致，且归集失败。

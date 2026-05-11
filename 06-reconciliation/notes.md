# 06 - 对账深度技术笔记

## 对账系统设计哲学

对账是钱包系统的"最后一道防线"：即使前面所有逻辑都有 bug，对账会发现链上和链下的不一致。
它不修复 bug，但它能发现问题，为人工介入提供时机。

## 差值单调性追踪

```
第1次对账：diff = +100 USDT（链上多100，可能漏记充值）
第2次对账：diff = +150 USDT（差值变大了！）
第3次对账：diff = +200 USDT（还在增大！）

判断：|200| > |150| > |100|，差值在单调增大
→ 说明不是暂时的数据延迟，而是持续性问题
→ 紧急告警：系统可能持续在漏记充值

对比正常恢复情况：
第1次：diff = +200
第2次：diff = +100（差值在减小，正在恢复）
第3次：diff = 0（恢复正常）
→ |0| < |100| < |200|，差值在单调减小
→ 这是正常的追赶过程，可以只记录日志
```

## 对账实现

```go
type Reconciler struct {
    chain    string
    symbol   string
    rpc      RPCClient
    repo     Repository
    alarm    Alarm
    prevDiff *big.Int  // 上次的差值（用于单调性判断）

    // 告警去重（防止频繁告警）
    lastAlertTime map[string]time.Time
    alertMu       sync.Mutex
}

func (r *Reconciler) Run(ctx context.Context) error {
    ticker := time.NewTicker(10 * time.Second)
    for {
        select {
        case <-ticker.C:
            r.check(ctx)
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}

func (r *Reconciler) check(ctx context.Context) {
    // Step 1: 链下余额（系统统计）
    offChainBalance, localHeight := r.repo.GetTotalBalance(ctx, r.chain, r.symbol)

    // Step 2: 链上余额（RPC 查询）
    onChainBalance, onChainHeight := r.rpc.GetContractTotalSupply(ctx, r.symbol)

    // Step 3: 高度差检查
    heightDiff := onChainHeight - localHeight
    if heightDiff > heightThreshold {
        r.alert("同步落后", fmt.Sprintf("本地高度 %d，链上高度 %d，差 %d 块",
            localHeight, onChainHeight, heightDiff))
        return  // 数据不可靠，本次对账跳过
    }

    // Step 4: 余额差
    diff := new(big.Int).Sub(onChainBalance, offChainBalance)
    absDiff := new(big.Int).Abs(diff)

    // Step 5: 差值超限告警
    if absDiff.Cmp(balanceThreshold) > 0 {
        direction := "链上多"
        if diff.Sign() < 0 { direction = "链下多" }
        r.alert("余额偏差", fmt.Sprintf("%s %s %s", direction, absDiff, r.symbol))
    }

    // Step 6: 差值单调性（关键！）
    if r.prevDiff != nil {
        prevAbs := new(big.Int).Abs(r.prevDiff)
        if absDiff.Cmp(prevAbs) > 0 && absDiff.Cmp(balanceThreshold) > 0 {
            r.alert("余额差值单调增大",
                fmt.Sprintf("前次 %s，本次 %s，差值在扩大！", prevAbs, absDiff))
        }
    }
    r.prevDiff = diff
}

// alert 带去重的告警（45分钟内同内容只发一次）
func (r *Reconciler) alert(title, msg string) {
    r.alertMu.Lock()
    defer r.alertMu.Unlock()

    key := title + msg
    if last, ok := r.lastAlertTime[key]; ok {
        if time.Since(last) < 45*time.Minute {
            return  // 去重，不重复告警
        }
    }
    r.lastAlertTime[key] = time.Now()
    r.alarm.Send(context.Background(), title, msg)
}
```

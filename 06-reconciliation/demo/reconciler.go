package main

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"
)

const (
	// heightThreshold 同步落后超过此块数时，本次对账数据不可靠，跳过
	heightThreshold int64 = 50
	// alertDedup 同一告警 45 分钟内不重复发送
	alertDedup = 45 * time.Minute
)

// balanceThreshold 余额偏差告警阈值（0.01 个代币的最小单位，需按精度换算）。
// demo 中简化为固定值，生产中应从 TokenConfig 读取。
var balanceThreshold = big.NewInt(10_000) // e.g. 0.01 USDT (6 decimals)

// Reconciler 对账器：定期比对链上余额与数据库余额，检测差值单调性。
//
// 设计来源：irwallet 的对账模块。
// ethfork 模式中，EVM 链只需实现 RPCClient 接口即可复用此逻辑。
type Reconciler struct {
	chain  string
	symbol string
	rpc    RPCClient
	repo   Repository
	alarm  Alarm

	// 差值单调性追踪
	prevDiff *big.Int

	// 告警去重（防止频繁告警）
	lastAlertTime map[string]time.Time
	alertMu       sync.Mutex
}

// NewReconciler 创建对账器。
func NewReconciler(chain, symbol string, rpc RPCClient, repo Repository, alarm Alarm) *Reconciler {
	return &Reconciler{
		chain:         chain,
		symbol:        symbol,
		rpc:           rpc,
		repo:          repo,
		alarm:         alarm,
		lastAlertTime: make(map[string]time.Time),
	}
}

// Run 启动定期对账循环（阻塞直到 ctx 取消）。
func (r *Reconciler) Run(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.check(ctx)
		}
	}
}

// CheckOnce 执行一次对账并返回结果（供测试和 cron 调用）。
func (r *Reconciler) CheckOnce(ctx context.Context) (*ReconcileResult, error) {
	return r.doCheck(ctx)
}

func (r *Reconciler) check(ctx context.Context) {
	result, err := r.doCheck(ctx)
	if err != nil {
		r.sendAlert(ctx, "对账异常", fmt.Sprintf("chain=%s symbol=%s err=%v", r.chain, r.symbol, err))
		return
	}
	r.evaluate(ctx, result)
}

// doCheck 执行实际的对账逻辑，返回结构化结果。
func (r *Reconciler) doCheck(ctx context.Context) (*ReconcileResult, error) {
	// Step 1: 获取链上高度（用于判断数据新鲜度）
	onChainHeight, err := r.rpc.GetBlockHeight(ctx)
	if err != nil {
		return nil, fmt.Errorf("get chain height: %w", err)
	}

	// Step 2: 获取本地同步高度
	localHeight, err := r.repo.GetLocalHeight(ctx, r.chain)
	if err != nil {
		return nil, fmt.Errorf("get local height: %w", err)
	}

	heightGap := onChainHeight - localHeight

	result := &ReconcileResult{
		Chain:         r.chain,
		Symbol:        r.symbol,
		OnChainHeight: onChainHeight,
		LocalHeight:   localHeight,
		HeightGap:     heightGap,
		CheckedAt:     time.Now(),
	}

	// Step 3: 高度差检查（数据不可靠时跳过余额对比）
	if heightGap > heightThreshold {
		result.Diff = big.NewInt(0)
		return result, nil
	}

	// Step 4: 获取所有受管地址
	addrs, err := r.repo.GetManagedAddresses(ctx, r.chain)
	if err != nil {
		return nil, fmt.Errorf("get managed addresses: %w", err)
	}

	// Step 5: 汇总链上余额 vs 链下余额
	onChainTotal := new(big.Int)
	offChainTotal := new(big.Int)

	for _, addr := range addrs {
		// 链上余额
		onBal, err := r.rpc.GetBalance(ctx, addr)
		if err != nil {
			return nil, fmt.Errorf("get on-chain balance %s: %w", addr, err)
		}
		onChainTotal.Add(onChainTotal, onBal)

		// 链下（数据库）余额
		offBal, err := r.repo.GetBalance(ctx, r.chain, r.symbol, addr)
		if err != nil {
			return nil, fmt.Errorf("get off-chain balance %s: %w", addr, err)
		}
		offChainTotal.Add(offChainTotal, offBal)
	}

	result.OnChainTotal = onChainTotal
	result.OffChainTotal = offChainTotal
	result.Diff = new(big.Int).Sub(onChainTotal, offChainTotal)

	return result, nil
}

// evaluate 根据对账结果发告警，并更新单调性状态。
func (r *Reconciler) evaluate(ctx context.Context, result *ReconcileResult) {
	// Step 6: 高度落后告警
	if result.HeightGap > heightThreshold {
		r.sendAlert(ctx, "同步落后",
			fmt.Sprintf("chain=%s localHeight=%d onChainHeight=%d gap=%d",
				r.chain, result.LocalHeight, result.OnChainHeight, result.HeightGap))
		r.prevDiff = nil // 高度不可靠时重置单调性
		return
	}

	absDiff := new(big.Int).Abs(result.Diff)

	// Step 7: 余额偏差告警
	if absDiff.Cmp(balanceThreshold) > 0 {
		direction := "链上多"
		if result.Diff.Sign() < 0 {
			direction = "链下多"
		}
		r.sendAlert(ctx, "余额偏差",
			fmt.Sprintf("chain=%s symbol=%s %s %s (on=%s off=%s)",
				r.chain, r.symbol, direction, absDiff, result.OnChainTotal, result.OffChainTotal))
	}

	// Step 8: 差值单调性检测（关键防线）
	//
	// 单调增大 → 持续性问题（漏记充值/重复提现），紧急告警
	// 单调减小 → 正在恢复（追赶区块），只记录日志
	if r.prevDiff != nil {
		prevAbs := new(big.Int).Abs(r.prevDiff)
		if absDiff.Cmp(prevAbs) > 0 && absDiff.Cmp(balanceThreshold) > 0 {
			r.sendAlert(ctx, "余额差值单调增大",
				fmt.Sprintf("chain=%s symbol=%s 前次偏差=%s 本次偏差=%s 偏差持续扩大！",
					r.chain, r.symbol, prevAbs, absDiff))
		}
	}

	r.prevDiff = new(big.Int).Set(result.Diff)
}

// sendAlert 带去重的告警（同一告警类型 45 分钟内只发一次）。
// key 使用 chain+symbol+title，不包含 msg 中的实时数值，
// 否则每次差值变化都会产生新 key，导致去重失效。
func (r *Reconciler) sendAlert(ctx context.Context, title, msg string) {
	r.alertMu.Lock()
	defer r.alertMu.Unlock()

	key := r.chain + "|" + r.symbol + "|" + title
	if last, ok := r.lastAlertTime[key]; ok {
		if time.Since(last) < alertDedup {
			return // 去重，不重复告警
		}
	}
	r.lastAlertTime[key] = time.Now()

	if err := r.alarm.Send(ctx, title, msg); err != nil {
		fmt.Printf("[reconciler] alarm send failed: %v\n", err)
	}
}

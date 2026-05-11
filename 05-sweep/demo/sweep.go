// Package main 归集系统 demo：四步流水线（取单→过滤→构建→补费）
package main

import (
	"context"
	"fmt"
	"math/big"
)

// Address 待归集的地址信息。
type Address struct {
	Addr    string
	UID     int64
	Symbol  string
	Balance *big.Int // 当前余额
}

// SweepTx 构建好的归集交易。
type SweepTx struct {
	From      string
	To        string // 热钱包地址
	Amount    *big.Int
	Symbol    string
	GasLimit  uint64
	GasPrice  *big.Int
	NeedsFee  bool // 是否需要先补 Gas
	FeeAmount *big.Int
}

// SweepConfig 归集配置。
type SweepConfig struct {
	Chain         string
	HotWallet     string
	MinBalance    *big.Int // 归集最小余额阈值
	KeepETH       *big.Int // 保留的最小主币量
	MaxGasPrice   *big.Int // 超过此 Gas Price 暂停归集
	SafeHeight    int64
}

// Repository 数据库接口（可 mock）。
type Repository interface {
	GetSweepCandidates(ctx context.Context, limit int) ([]*Address, error)
	GetLastDepositHeight(ctx context.Context, addr string) (int64, error)
	MarkSwept(ctx context.Context, addr string, txHash string) error
}

// RPCClientSweep RPC 接口（可 mock）。
type RPCClientSweep interface {
	GetBalance(ctx context.Context, addr string) (*big.Int, error)
	EstimateGas(ctx context.Context, from, to string, data []byte) (uint64, error)
	GetGasPrice(ctx context.Context) (*big.Int, error)
	SendTransaction(ctx context.Context, tx *SweepTx) (string, error)
}

// Pipeline 四步归集流水线。
type Pipeline struct {
	repo   Repository
	rpc    RPCClientSweep
	config *SweepConfig
}

// NewPipeline 创建归集流水线。
func NewPipeline(repo Repository, rpc RPCClientSweep, config *SweepConfig) *Pipeline {
	return &Pipeline{repo: repo, rpc: rpc, config: config}
}

// Run 执行完整的归集流水线（channel 驱动）。
func (p *Pipeline) Run(ctx context.Context) error {
	stream := p.retrieve(ctx)
	filtered := p.filter(ctx, stream)
	built := p.build(ctx, filtered)
	return p.fee(ctx, built)
}

// retrieve 查询余额超过阈值的待归集地址。
func (p *Pipeline) retrieve(ctx context.Context) <-chan *Address {
	ch := make(chan *Address, 10)
	go func() {
		defer close(ch)
		addrs, err := p.repo.GetSweepCandidates(ctx, 150)
		if err != nil {
			fmt.Printf("[sweep] retrieve error: %v\n", err)
			return
		}
		for _, addr := range addrs {
			select {
			case ch <- addr:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

// filter 过滤：SafeHeight 检查 + 基本校验。
func (p *Pipeline) filter(ctx context.Context, in <-chan *Address) <-chan *Address {
	ch := make(chan *Address, 10)
	go func() {
		defer close(ch)
		for addr := range in {
			// SafeHeight 检查：防止归集重组中的充值
			lastHeight, err := p.repo.GetLastDepositHeight(ctx, addr.Addr)
			if err != nil {
				fmt.Printf("[sweep] get last deposit height %s: %v\n", addr.Addr, err)
				continue
			}
			if lastHeight > p.config.SafeHeight {
				fmt.Printf("[sweep] skip %s: last deposit height %d > safeHeight %d\n",
					addr.Addr, lastHeight, p.config.SafeHeight)
				continue
			}

			// 余额检查
			if addr.Balance.Cmp(p.config.MinBalance) < 0 {
				continue
			}

			select {
			case ch <- addr:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

// build 构建归集交易（含 Gas 估算）。
func (p *Pipeline) build(ctx context.Context, in <-chan *Address) <-chan *SweepTx {
	ch := make(chan *SweepTx, 10)
	go func() {
		defer close(ch)

		gasPrice, err := p.rpc.GetGasPrice(ctx)
		if err != nil {
			fmt.Printf("[sweep] get gas price: %v\n", err)
			return
		}

		// Gas Price 超限，暂停归集
		if gasPrice.Cmp(p.config.MaxGasPrice) > 0 {
			fmt.Printf("[sweep] gas price %s > max %s, pausing\n", gasPrice, p.config.MaxGasPrice)
			return
		}

		for addr := range in {
			amount := new(big.Int).Set(addr.Balance)

			// 主币归集：保留 KeepETH
			if addr.Symbol == "ETH" {
				amount.Sub(amount, p.config.KeepETH)
				if amount.Sign() <= 0 {
					continue
				}
			}

			gasLimit := uint64(65000) // ERC20 归集约 65000 gas
			if addr.Symbol == "ETH" {
				gasLimit = 21000
			}

			tx := &SweepTx{
				From:     addr.Addr,
				To:       p.config.HotWallet,
				Amount:   amount,
				Symbol:   addr.Symbol,
				GasLimit: gasLimit,
				GasPrice: new(big.Int).Set(gasPrice),
			}

			select {
			case ch <- tx:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

// fee 补费检查并广播：主币不足时先从热钱包补 Gas。
func (p *Pipeline) fee(ctx context.Context, in <-chan *SweepTx) error {
	for tx := range in {
		// 检查用户地址是否有足够 ETH 支付 Gas
		ethBalance, err := p.rpc.GetBalance(ctx, tx.From)
		if err != nil {
			fmt.Printf("[sweep] get eth balance %s: %v\n", tx.From, err)
			continue
		}

		gasCost := new(big.Int).Mul(
			new(big.Int).SetUint64(tx.GasLimit),
			tx.GasPrice,
		)

		if ethBalance.Cmp(gasCost) < 0 {
			feeAmount := calcFeeAmount(tx.GasPrice)
			if feeAmount.Sign() == 0 {
				// Gas Price 过高，连补费都不划算，跳过
				fmt.Printf("[sweep] gas too high to fee %s, skip\n", tx.From)
				continue
			}
			// 发送补费交易（热钱包 → 用户地址）
			feeTx := &SweepTx{
				From:      p.config.HotWallet,
				To:        tx.From,
				Amount:    feeAmount,
				Symbol:    "ETH",
				GasLimit:  21000,
				GasPrice:  new(big.Int).Set(tx.GasPrice),
				NeedsFee:  true,
				FeeAmount: feeAmount,
			}
			feeHash, err := p.rpc.SendTransaction(ctx, feeTx)
			if err != nil {
				fmt.Printf("[sweep] fee tx failed %s: %v\n", tx.From, err)
				continue
			}
			// 本轮不发归集，等补费上链（≥1个确认）后下次 sweep 循环再处理。
			// 生产中：将 feeHash 写入 pending_fee_tx 表，
			// 下次 sweep 前先调用 GetTransactionReceipt(feeHash) 确认已上链。
			fmt.Printf("[sweep] fee sent hotWallet→%s amount=%s hash=%s (归集下轮重试)\n",
				tx.From, feeAmount, feeHash)
			continue
		}

		// ETH 充足，直接广播归集交易
		txHash, err := p.rpc.SendTransaction(ctx, tx)
		if err != nil {
			fmt.Printf("[sweep] send tx %s→%s: %v\n", tx.From, tx.To, err)
			continue
		}

		fmt.Printf("[sweep] swept: %s→%s, symbol=%s, amount=%s, txHash=%s\n",
			tx.From, tx.To, tx.Symbol, tx.Amount, txHash)

		p.repo.MarkSwept(ctx, tx.From, txHash)
	}
	return nil
}

// calcFeeAmount 根据 Gas Price 动态计算补费金额。
func calcFeeAmount(gasPrice *big.Int) *big.Int {
	gwei := new(big.Int).Div(gasPrice, big.NewInt(1_000_000_000)).Int64()
	var ethAmount int64
	switch {
	case gwei <= 10:
		ethAmount = 6_000_000_000_000_000 // 0.006 ETH
	case gwei <= 50:
		ethAmount = 30_000_000_000_000_000 // 0.03 ETH
	case gwei <= 200:
		ethAmount = 120_000_000_000_000_000 // 0.12 ETH
	default:
		ethAmount = 0 // Gas Price 太高，暂停
	}
	return big.NewInt(ethAmount)
}

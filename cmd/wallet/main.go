// Package main 钱包业务服务入口（wallet service）。
//
// 职责：区块同步 / 充值检测 / 提现执行 / 归集 / 对账。
//
// 安全边界：本服务不持有私钥。
//   所有签名请求通过 X25519 加密通道发往独立的 keyman 服务（cmd/keyman）。
//   即使 wallet 服务被攻破，攻击者也无法获取私钥。
//
// 部署：见 docs/deployment.md。每条链运行一个独立的 ChainWorker goroutine。
//
// 运行（开发环境）：go run ./cmd/wallet/
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/yys9517/exchange-wallet/internal/alarm"
	"github.com/yys9517/exchange-wallet/internal/coinset"
)

// ─────────────────────────────────────────────
// 接口定义（由各模块具体实现，此处仅声明契约）
// ─────────────────────────────────────────────

// ChainWorker 每条链的业务处理单元。
// 生产实现：
//   - syncWorker  → 02-block-sync/demo/syncer.go
//   - depositWorker 内嵌在 syncWorker 中
//   - sweepWorker → 05-sweep/demo/sweep.go
//   - reconWorker → 06-reconciliation/demo/reconciler.go
type ChainWorker interface {
	Run(ctx context.Context) error
	ChainName() string
}

// ─────────────────────────────────────────────
// WalletService：服务组装与生命周期管理
// ─────────────────────────────────────────────

// WalletService 组装所有链的 ChainWorker，统一管理启动和关闭。
type WalletService struct {
	workers []ChainWorker
	alarm   alarm.Alarm
	logger  *slog.Logger
}

// newWalletService 从配置初始化所有组件。
// 生产中：
//   cfg  := config.Load("config/wallet-dev.yaml")
//   rpc  := ethrpc.NewMultiClient(cfg.RPC)
//   repo := walletdb.New(cfg.DB)
//   al   := alarm.NewLarkAlarm(cfg.Alarm.LarkWebhook, logger)
func newWalletService(logger *slog.Logger) *WalletService {
	// irwallet 模式：每条链用同一套 ethfork 代码 + 不同 ChainConfig
	supportedChains := []coinset.Chain{
		coinset.ETH,
		coinset.BSC,
		coinset.Polygon,
	}

	workers := make([]ChainWorker, 0, len(supportedChains))
	for _, c := range supportedChains {
		workers = append(workers, &stubChainWorker{chain: c, logger: logger})
	}

	return &WalletService{
		workers: workers,
		alarm:   &alarm.NoopAlarm{}, // 生产：alarm.NewLarkAlarm(webhookURL, logger)
		logger:  logger,
	}
}

// Run 启动所有 ChainWorker，阻塞直到收到 SIGTERM/SIGINT 或发生不可恢复错误。
func (s *WalletService) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 信号处理
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	// 启动所有 ChainWorker
	errCh := make(chan error, len(s.workers))
	var wg sync.WaitGroup

	for _, w := range s.workers {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.logger.Info("chain worker starting", "chain", w.ChainName())
			if err := w.Run(ctx); err != nil && err != context.Canceled {
				s.logger.Error("chain worker fatal error",
					"chain", w.ChainName(), "error", err)
				// 关键链故障触发全局告警
				s.alarm.Send(ctx, "ChainWorker异常",
					fmt.Sprintf("chain=%s err=%v", w.ChainName(), err))
				errCh <- err
			}
			s.logger.Info("chain worker stopped", "chain", w.ChainName())
		}()
	}

	// 等待信号或错误
	select {
	case sig := <-sigCh:
		s.logger.Info("received shutdown signal", "signal", sig)
	case err := <-errCh:
		s.logger.Error("fatal error, initiating shutdown", "error", err)
	}
	cancel()

	// 优雅关闭（超时 30 秒）
	s.logger.Info("waiting for workers to finish...")
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("graceful shutdown complete")
	case <-time.After(30 * time.Second):
		s.logger.Warn("shutdown timeout: some workers did not stop in time")
	}
	return nil
}

// ─────────────────────────────────────────────
// stubChainWorker（demo 用，生产中替换为真实实现）
// ─────────────────────────────────────────────

type stubChainWorker struct {
	chain  coinset.Chain
	logger *slog.Logger
}

func (w *stubChainWorker) ChainName() string { return w.chain.Name }

func (w *stubChainWorker) Run(ctx context.Context) error {
	// 生产中组装方式（ethfork 模式，一套代码多条链）：
	//
	//   rpc      := ethrpc.NewMultiClient(rpcEndpoints)
	//   repo     := walletdb.New(db)
	//   syncer   := sync.NewSyncer(w.chain.Name, w.chain.Confirms, rpc, repo, alarm, logger)
	//   pipeline := deposit.NewPipeline(depositCfg, rpc)     // 7层防护
	//   sweeper  := sweep.NewPipeline(repo, rpc, sweepCfg)
	//   recon    := reconcile.New(w.chain.Name, rpc, repo, alarm)
	//
	//   // 并发运行所有子任务
	//   errg, ctx := errgroup.WithContext(ctx)
	//   errg.Go(func() error { return syncer.Run(ctx) })
	//   errg.Go(func() error { return sweeper.RunEvery(ctx, 5*time.Minute) })
	//   errg.Go(func() error { return recon.Run(ctx, 10*time.Minute) })
	//   return errg.Wait()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// stub: 生产中调用 syncer.tick()
		}
	}
}

// ─────────────────────────────────────────────
// main
// ─────────────────────────────────────────────

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("wallet service starting",
		"chains", "ETH,BSC,POLYGON",
		"pid", os.Getpid(),
		"note", "signing delegated to keyman service (X25519 encrypted channel)",
	)

	svc := newWalletService(logger)
	if err := svc.Run(); err != nil {
		logger.Error("service exited with error", "error", err)
		os.Exit(1)
	}

	logger.Info("wallet service stopped")
}

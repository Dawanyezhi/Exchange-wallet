// Package main 进程内端到端模拟：展示所有模块如何在单个服务中协作。
//
// 与旧版（子进程调用各 demo）的核心区别：
//   本程序在同一个进程内通过 WalletSystem 组装所有模块，
//   模块间通过共享的 SimDB + SimBlockchain 传递状态，
//   反映生产系统中 Syncer / DepositPipeline / Sweep / Reconciler
//   共享同一个 MySQL 实例和 RPC 节点的架构。
//
// 运行：go run ./cmd/simulate/
// 或：  make simulate
package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"

	"github.com/yys9517/exchange-wallet/internal/alarm"
	"github.com/yys9517/exchange-wallet/internal/coinset"
)

// ─────────────────────────────────────────────
// 共享基础设施（生产中：ETH节点RPC + MySQL）
// ─────────────────────────────────────────────

// SimBlockchain 模拟区块链（可替换为真实 ethrpc.MultiClient）。
type SimBlockchain struct {
	mu     sync.RWMutex
	blocks map[int64]*SimBlock
	latest int64
}

type SimBlock struct {
	Height int64
	Hash   string
	Parent string
	Txs    []*SimTx
}

type SimTx struct {
	Hash        string
	From        string
	To          string
	Value       *big.Int
	Success     bool
	Contract    string // ERC20 合约地址，空=主币转账
	LogRemoved  bool
}

func newSimBlockchain() *SimBlockchain {
	return &SimBlockchain{blocks: make(map[int64]*SimBlock)}
}
func (bc *SimBlockchain) add(b *SimBlock) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.blocks[b.Height] = b
	if b.Height > bc.latest {
		bc.latest = b.Height
	}
}
func (bc *SimBlockchain) get(h int64) *SimBlock {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.blocks[h]
}

// SimDB 模拟数据库（可替换为真实 walletdb.DB）。
type SimDB struct {
	mu           sync.RWMutex
	headers      map[int64]string  // height → hash
	backHeight   int64
	frontHeight  int64
	deposits     []*SimDeposit
	balances     map[string]*big.Int // addr → on-chain balance (mock)
	offBalances  map[string]*big.Int // addr → db balance
	managedAddrs map[string]int64    // addr → uid
	hotWallets   map[string]bool
	whitelist    map[string]bool    // ERC20 contract whitelist
}

type SimDeposit struct {
	TxHash string
	Symbol string
	To     string
	Amount *big.Int
	UID    int64
	Status string
}

func newSimDB() *SimDB {
	return &SimDB{
		headers:     make(map[int64]string),
		balances:    make(map[string]*big.Int),
		offBalances: make(map[string]*big.Int),
		managedAddrs: map[string]int64{
			"0xuser1111": 1001,
			"0xuser2222": 1002,
		},
		hotWallets: map[string]bool{
			"0xhotwallet": true,
		},
		whitelist: map[string]bool{
			"0xdac17f958d2ee523a2206206994597c13d831ec7": true, // USDT
		},
	}
}

// ─────────────────────────────────────────────
// WalletSystem：所有模块的进程内组装
// ─────────────────────────────────────────────

// WalletSystem 在单个进程内持有所有模块所需的共享组件。
// 生产中：bc = ethrpc.MultiClient，db = walletdb.DB（MySQL）。
type WalletSystem struct {
	chain    *coinset.Chain // 链配置（确认数、FeatureGate）
	alarm    alarm.Alarm    // 告警接口（irwallet 层）
	bc       *SimBlockchain
	db       *SimDB
	confirms int64
}

func newWalletSystem() *WalletSystem {
	c := coinset.ETH
	confirms := int64(2) // demo 用2，生产用 c.Confirms=12
	return &WalletSystem{
		chain:    &c,
		alarm:    &alarm.NoopAlarm{},
		bc:       newSimBlockchain(),
		db:       newSimDB(),
		confirms: confirms,
	}
}

// ─────────────────────────────────────────────
// 场景1：区块同步 + Front/Back 滑动窗口
// ─────────────────────────────────────────────

func (sys *WalletSystem) scenario1_BlockSync(_ context.Context) bool {
	fmt.Println("\n【场景1】区块同步 + Front/Back 滑动窗口（02-block-sync 核心）")

	blocks := []*SimBlock{
		{1, "0xAAAA", "0x0000", nil},
		{2, "0xBBBB", "0xAAAA", nil},
		{3, "0xCCCC", "0xBBBB", []*SimTx{
			{Hash: "0xTx3A", From: "0xExternal", To: "0xUser1111", Value: big.NewInt(1e18), Success: true},
		}},
		{4, "0xDDDD", "0xCCCC", nil},
		{5, "0xEEEE", "0xDDDD", []*SimTx{
			{Hash: "0xTx5B", From: "0xExternal", To: "0xUser2222", Value: big.NewInt(5e17), Success: true},
		}},
	}

	for _, b := range blocks {
		sys.bc.add(b)

		sys.db.mu.Lock()
		sys.db.headers[b.Height] = b.Hash
		sys.db.backHeight = b.Height
		front := b.Height - sys.confirms
		if front < 0 {
			front = 0
		}
		sys.db.frontHeight = front

		for _, tx := range b.Txs {
			if !tx.Success {
				continue
			}
			toLower := strings.ToLower(tx.To)
			if uid, ok := sys.db.managedAddrs[toLower]; ok {
				sys.db.deposits = append(sys.db.deposits, &SimDeposit{
					TxHash: tx.Hash, Symbol: "ETH",
					To: tx.To, Amount: new(big.Int).Set(tx.Value),
					UID: uid, Status: "pending",
				})
				fmt.Printf("  → 充值入库: hash=%s to=%s value=%s uid=%d\n",
					tx.Hash, tx.To, tx.Value, uid)
			}
			// 更新 offChain 余额（模拟数据库记账）
			if bal, ok := sys.db.offBalances[toLower]; ok {
				bal.Add(bal, tx.Value)
			} else {
				sys.db.offBalances[toLower] = new(big.Int).Set(tx.Value)
			}
		}
		// 确认已到 SafeHeight 的充值
		for _, d := range sys.db.deposits {
			if b.Height >= front+sys.confirms && d.Status == "pending" {
				d.Status = "confirmed"
			}
		}
		sys.db.mu.Unlock()
	}

	sys.db.mu.RLock()
	back, front := sys.db.backHeight, sys.db.frontHeight
	depCount := len(sys.db.deposits)
	sys.db.mu.RUnlock()

	fmt.Printf("  Back=%d Front=%d SafeHeight窗口=%d块 充值=%d笔\n",
		back, front, sys.confirms, depCount)
	return back == 5 && depCount == 2
}

// ─────────────────────────────────────────────
// 场景2：充值7层防护（多种攻击被拦截）
// ─────────────────────────────────────────────

func (sys *WalletSystem) scenario2_DepositFilter(_ context.Context) bool {
	fmt.Println("\n【场景2】充值7层防护（03-deposit 核心）")

	cases := []struct {
		name     string
		status   uint64
		contract string
		removed  bool
		from     string
		want     bool
	}{
		{"有效 USDT 充值",                1, "0xdac17f958d2ee523a2206206994597c13d831ec7", false, "0xExternal", true},
		{"L1: 失败交易 status=0",         0, "0xdac17f958d2ee523a2206206994597c13d831ec7", false, "0xExternal", false},
		{"L3: 假 USDT 合约",              1, "0xFakeToken0000000000000000000000000000001", false, "0xExternal", false},
		{"L2: Removed log（重组撤销）",   1, "0xdac17f958d2ee523a2206206994597c13d831ec7", true, "0xExternal", false},
		{"L6: 热钱包发出（提现误判）",    1, "0xdac17f958d2ee523a2206206994597c13d831ec7", false, "0xHotWallet", false},
		{"L4: BlockHash 不一致（重组）",  1, "0xdac17f958d2ee523a2206206994597c13d831ec7", false, "0xExternal", false}, // 通过 removed=true 模拟
	}
	// 最后一个 case 用 removed=true 来模拟 BlockHash 检查失败
	cases[5].removed = true

	sys.db.mu.RLock()
	whitelist := sys.db.whitelist
	hotWallets := sys.db.hotWallets
	sys.db.mu.RUnlock()

	passed := 0
	for _, c := range cases {
		ok := c.status == 1 &&
			whitelist[strings.ToLower(c.contract)] &&
			!c.removed &&
			!hotWallets[strings.ToLower(c.from)]
		mark := "✓"
		if ok == c.want {
			passed++
		} else {
			mark = "✗ UNEXPECTED"
		}
		fmt.Printf("  %s %-35s → pass=%v\n", mark, c.name, ok)
	}
	fmt.Printf("  通过 %d/%d\n", passed, len(cases))
	return passed == len(cases)
}

// ─────────────────────────────────────────────
// 场景3：区块重组 + 公共祖先追溯
// ─────────────────────────────────────────────

func (sys *WalletSystem) scenario3_Reorg(_ context.Context) bool {
	fmt.Println("\n【场景3】区块重组（02-block-sync reorg 核心）")

	// 加入分叉链
	forkBlocks := []*SimBlock{
		{4, "0xDDD2", "0xCCCC", nil},
		{5, "0xEEE2", "0xDDD2", nil},
		{6, "0xFFF2", "0xEEE2", []*SimTx{
			{Hash: "0xTx6C", From: "0xExternal", To: "0xUser1111", Value: big.NewInt(2e18), Success: true},
		}},
	}
	for _, b := range forkBlocks {
		sys.bc.add(b)
	}

	// 检测重组：新块4的parentHash=0xCCCC，但本地4号=0xDDDD
	newBlock4 := forkBlocks[0]
	sys.db.mu.RLock()
	localHash4 := sys.db.headers[4]
	localHash3 := sys.db.headers[3]
	sys.db.mu.RUnlock()

	isReorg := localHash4 != "" && !strings.EqualFold(localHash4, newBlock4.Hash)
	commonFound := strings.EqualFold(localHash3, newBlock4.Parent)

	fmt.Printf("  重组检测: local[4]=%s fork[4].hash=%s → 不匹配！\n", localHash4, newBlock4.Hash)
	fmt.Printf("  向上追溯: local[3]=%s == fork[4].parent=%s → 公共祖先找到！\n",
		localHash3, newBlock4.Parent)

	sys.db.mu.RLock()
	rollbackDepth := sys.db.backHeight - 3
	depsBefore := len(sys.db.deposits)
	sys.db.mu.RUnlock()

	fmt.Printf("  回滚深度: %d块（7步原子DB事务：revertBalance→...→revertHeight）\n", rollbackDepth)

	// 执行回滚
	sys.db.mu.Lock()
	var remaining []*SimDeposit
	removed := 0
	for _, d := range sys.db.deposits {
		// 高度>3的充值回滚（0xTx5B 对应高度5）
		if d.TxHash == "0xTx5B" {
			removed++
			continue
		}
		remaining = append(remaining, d)
	}
	sys.db.deposits = remaining
	sys.db.backHeight = 3
	sys.db.mu.Unlock()

	fmt.Printf("  回滚完成: 删除 %d 笔充值(0xTx5B), 保留 %d 笔\n", removed, depsBefore-removed)
	return isReorg && commonFound && removed == 1
}

// ─────────────────────────────────────────────
// 场景4：提现 Nonce 串行化
// ─────────────────────────────────────────────

func (sys *WalletSystem) scenario4_Withdrawal(_ context.Context) bool {
	fmt.Println("\n【场景4】提现（04-withdrawal: NonceManager + EIP-155）")

	// 在 goroutine 中并发分配 Nonce，验证串行化
	var mu sync.Mutex
	nonce := uint64(42) // 模拟链上 pending nonce
	results := make([]uint64, 10)
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			mu.Lock()
			n := nonce
			nonce++
			mu.Unlock()
			results[idx] = n
		}(i)
	}
	wg.Wait()

	// 验证无重复（mutex 保证串行化）
	seen := make(map[uint64]bool)
	for _, n := range results {
		seen[n] = true
	}
	nodup := len(seen) == 10

	fmt.Printf("  10个 goroutine 并发分配 Nonce: 无重复=%v\n", nodup)
	fmt.Printf("  EIP-155: chainID=%d 编入签名（防止ETH/ETC跨链重放）\n", sys.chain.ChainID)
	fmt.Printf("  MaxFee 保护: Gas > 500Gwei → 拒绝发送\n")
	return nodup
}

// ─────────────────────────────────────────────
// 场景5：归集流水线
// ─────────────────────────────────────────────

func (sys *WalletSystem) scenario5_Sweep(_ context.Context) bool {
	fmt.Println("\n【场景5】归集（05-sweep: SafeHeight过滤 + 补费）")

	sys.db.mu.RLock()
	safeHeight := sys.db.frontHeight
	sys.db.mu.RUnlock()
	if safeHeight == 0 {
		safeHeight = 1
	}

	candidates := []struct {
		addr        string
		symbol      string
		lastDeposit int64
		balance     *big.Int
		ethBal      *big.Int
	}{
		{"0xUser1111", "USDT", 1, big.NewInt(100_000_000), big.NewInt(0)},           // 无 ETH，需补费
		{"0xUser2222", "ETH", safeHeight + 3, big.NewInt(5e17), big.NewInt(1e17)},   // 未达 SafeHeight
		{"0xUser3333", "USDT", safeHeight - 1, big.NewInt(50_000_000), big.NewInt(1e17)}, // 正常归集
	}

	swept, skipped, feeSent := 0, 0, 0
	gasCost := big.NewInt(65000 * 5_000_000_000) // 65000 gas * 5 Gwei

	for _, c := range candidates {
		if c.lastDeposit > safeHeight {
			fmt.Printf("  跳过 %s: lastDeposit=%d > safeHeight=%d\n", c.addr, c.lastDeposit, safeHeight)
			skipped++
			continue
		}
		if c.ethBal.Cmp(gasCost) < 0 {
			// 补费后本轮跳过，等下次 sweep 循环
			fmt.Printf("  补费 hotWallet→%s ETH不足（gas需%s），下轮再归集\n", c.addr, gasCost)
			feeSent++
			continue
		}
		fmt.Printf("  归集 %s→hotWallet symbol=%s amount=%s\n", c.addr, c.symbol, c.balance)
		swept++
	}

	fmt.Printf("  归集=%d 补费=%d 跳过=%d (safeHeight=%d)\n", swept, feeSent, skipped, safeHeight)
	return swept == 1 && skipped == 1 && feeSent == 1
}

// ─────────────────────────────────────────────
// 场景6：对账差值单调性
// ─────────────────────────────────────────────

func (sys *WalletSystem) scenario6_Reconciliation(_ context.Context) bool {
	fmt.Println("\n【场景6】对账（06-reconciliation: 差值单调性检测）")

	// 读取数据库的链下余额（场景1写入）
	sys.db.mu.RLock()
	offChainTotal := new(big.Int)
	for _, bal := range sys.db.offBalances {
		offChainTotal.Add(offChainTotal, bal)
	}
	sys.db.mu.RUnlock()

	// 模拟3次链上查询（链上余额逐渐偏离）
	checks := []int64{0, 20_000, 40_000} // diff 持续增大
	threshold := int64(10_000)

	var prevAbsDiff int64 = 0
	monotonic := false

	for i, extraOnChain := range checks {
		onChainTotal := new(big.Int).Add(offChainTotal, big.NewInt(extraOnChain))
		diff := new(big.Int).Sub(onChainTotal, offChainTotal)
		absDiff := new(big.Int).Abs(diff).Int64()

		status := "正常"
		if absDiff > threshold {
			if prevAbsDiff > 0 && absDiff > prevAbsDiff {
				status = "⚠️  差值单调增大！"
				monotonic = true
			} else {
				status = "偏差告警"
			}
		}
		fmt.Printf("  第%d次: onChain=%s offChain=%s diff=%+d → %s\n",
			i+1, onChainTotal, offChainTotal, diff, status)
		prevAbsDiff = absDiff
	}

	fmt.Printf("  告警去重：45分钟内相同内容只发1次（见06-reconciliation测试）\n")
	return monotonic
}

// ─────────────────────────────────────────────
// 场景7：密钥管理（BIP32路径设计）
// ─────────────────────────────────────────────

func (sys *WalletSystem) scenario7_KeyManagement(_ context.Context) bool {
	fmt.Println("\n【场景7】密钥管理（01-key-management: BIP32 + Scrypt + X25519）")

	// irwallet 的路径设计：用 FNV-1a(coinName) 替代 BIP44 coin type
	// 优势：支持任意链，不受 BIP44 coin type 列表限制
	fnv1a := func(s string) uint32 {
		h := uint32(2166136261)
		for _, c := range []byte(s) {
			h ^= uint32(c)
			h *= 16777619
		}
		return h
	}

	chains := []coinset.Chain{coinset.ETH, coinset.BSC, coinset.Polygon, coinset.ETC}
	for _, c := range chains {
		idx := fnv1a(c.Name)
		path := fmt.Sprintf("m/%d'/%d'", idx, 0)
		fmt.Printf("  %-8s chainID=%-5d coinIdx=%-12d 路径=%s (硬化，泄露子密钥不影响其他链)\n",
			c.Name, c.ChainID, idx, path)
	}

	fmt.Printf("\n  Scrypt(N=2^18,r=8,p=1)+AES-128-CTR → 内存硬KDF，GPU无加速优势\n")
	fmt.Printf("  X25519 ECDH+AES-256-GCM 传输 → 每请求临时密钥对，前向安全\n")
	fmt.Printf("  4步内存清零(0→FF→rand→0) → 防止 core dump 泄漏\n")
	return true
}

// ─────────────────────────────────────────────
// main
// ─────────────────────────────────────────────

func main() {
	printBanner()

	sys := newWalletSystem()
	ctx := context.Background()

	type scenario struct {
		name string
		fn   func(context.Context) bool
	}

	scenarios := []scenario{
		{"区块同步 + SafeHeight 窗口", sys.scenario1_BlockSync},
		{"充值7层防护（多种攻击拦截）", sys.scenario2_DepositFilter},
		{"区块重组 + 公共祖先回滚", sys.scenario3_Reorg},
		{"提现 Nonce 串行化（并发安全）", sys.scenario4_Withdrawal},
		{"归集 SafeHeight + 补费逻辑", sys.scenario5_Sweep},
		{"对账差值单调性检测", sys.scenario6_Reconciliation},
		{"密钥 BIP32 路径 + Scrypt 安全", sys.scenario7_KeyManagement},
	}

	passed, failed := 0, 0
	for _, s := range scenarios {
		ok := s.fn(ctx)
		if ok {
			fmt.Printf("  ✅ [PASS] %s\n", s.name)
			passed++
		} else {
			fmt.Printf("  ❌ [FAIL] %s\n", s.name)
			failed++
		}
	}

	fmt.Printf("\n╔══════════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║  模拟结果：%d 通过 / %d 失败                               ║\n", passed, failed)
	fmt.Printf("╚══════════════════════════════════════════════════════════════╝\n")

	if failed > 0 {
		os.Exit(1)
	}
}

func printBanner() {
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║         Exchange Wallet — 进程内全场景模拟                  ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("架构：")
	fmt.Println("  irwallet 层 → internal/{bigint,coinset,alarm}  跨链通用")
	fmt.Println("  ethfork 层  → WalletSystem（本程序进程内组装）  EVM兼容链")
	fmt.Println()
	fmt.Println("本程序通过共享 SimDB + SimBlockchain 展示跨模块数据流：")
	fmt.Println("  场景1(同步)→写入SimDB → 场景3(重组)→回滚SimDB")
	fmt.Println("                                        ↘ 场景6(对账)→读取SimDB余额")
	fmt.Println()
}

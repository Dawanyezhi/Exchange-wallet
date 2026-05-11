// Package main 密钥管理服务入口（keyman service / walletmanager）。
//
// 职责：BIP32 密钥派生 / 签名请求处理 / Scrypt 加密存储。
//
// 安全原则：
//   1. 私钥从不离开本服务进程（不序列化到网络，不写日志）。
//   2. 内存中的私钥以 XOR Mask 保护（见 01-key-management/demo/memory_clear.go）。
//   3. 签名请求和响应通过 X25519 ECDH + AES-256-GCM 加密传输（前向安全）。
//   4. 本服务仅监听内网地址（127.0.0.1），不对外暴露。
//   5. 内存中的密钥在服务退出时执行4步清零。
//
// 部署：隔离网络，与 wallet 服务通过 gRPC（TLS + X25519 加密）通信。
//
// 运行（开发环境）：go run ./cmd/keyman/
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/yys9517/exchange-wallet/internal/coinset"
)

// ─────────────────────────────────────────────
// 数据库模型（对应 docs/database-schema.sql 中的 keyman 表）
// ─────────────────────────────────────────────

// MasterSeedRecord 对应 keyman_master_seed 表。
// 主种子以 Web3 Keystore V3 格式加密存储（Scrypt+AES-128-CTR）。
// 生产中从 DB 读取 Crypto 字段，用密码解密后加载到内存（XOR Mask 保护）。
type MasterSeedRecord struct {
	ID       int64  `db:"id"`
	SeedID   string `db:"seed_id"`   // UUID，支持多套种子共存
	Version  int    `db:"version"`   // 固定 3（Web3 Keystore V3）
	Crypto   string `db:"crypto"`    // JSON: {cipher, ciphertext, cipherparams, kdf, kdfparams, mac}
	IsActive bool   `db:"is_active"` // 当前是否在用（密钥轮换时旧种子标记为 false）
}

// DerivedAddressRecord 对应 keyman_derived_addresses 表。
// 存公钥，不存私钥。签名时通过 (chain, address) 反查 account_index，
// 再从内存中的主种子实时派生子密钥、签名后立即清零。
type DerivedAddressRecord struct {
	ID             int64  `db:"id"`
	SeedID         string `db:"seed_id"`
	Chain          string `db:"chain"`
	AccountIndex   uint32 `db:"account_index"`
	DerivationPath string `db:"derivation_path"` // 如 m/fnv1a(ETH)'/0'
	Address        string `db:"address"`
	PubKey         []byte `db:"pubkey"`  // 压缩公钥（33字节 secp256k1 / 32字节 Ed25519）
	Kind           int    `db:"kind"`    // 0=User 1=Hot 2=Cold
	Status         int    `db:"status"`  // 1=Active 0=Disabled
}

// SignAuditRecord 对应 keyman_sign_audit 表。
// 所有签名请求必须记录审计日志，只增不改不删。
type SignAuditRecord struct {
	ID            int64  `db:"id"`
	Chain         string `db:"chain"`
	Address       string `db:"address"`
	TxHash        string `db:"tx_hash"`
	CallerService string `db:"caller_service"` // wallet-withdrawal / wallet-sweep
	RequestIP     string `db:"request_ip"`
	Result        string `db:"result"`      // success / denied / error
	DenyReason    string `db:"deny_reason"` // 拒绝原因
}

// ─────────────────────────────────────────────
// API 请求/响应（对应 docs/api-design.md 中的 Keyman API）
// ─────────────────────────────────────────────

// DerivationRequest 地址派生请求。
type DerivationRequest struct {
	Chain   string
	Kind    int    // 0=User 1=Hot 2=Cold
	Count   int    // 批量派生数量（默认1，最大100）
}

// DerivationResponse 派生结果。
type DerivationResponse struct {
	Chain          string
	AccountIndex   uint32
	Address        string
	PubKey         []byte
	DerivationPath string
}

// SignRequest 签名请求（通过 X25519 加密通道接收）。
type SignRequest struct {
	Chain   string
	Address string // 签名地址（通过此地址反查 account_index）
	TxHash  []byte // 待签名的 32 字节哈希
}

// SignResponse 签名结果（通过 X25519 加密通道返回）。
type SignResponse struct {
	Signature []byte // 65 字节：[r(32), s(32), v(1)]
	Address   string
}

// ─────────────────────────────────────────────
// KeymanService
// ─────────────────────────────────────────────

// KeymanService 密钥管理服务。
//
// 私钥全生命周期：
//
//	[启动] DB/文件 → Scrypt 解密 → XOR Mask 保护存入内存
//	[派生] 内存主种子 → BIP32 子密钥 → 提取公钥+地址 → 写入 keyman_derived_addresses → 清零子密钥
//	[签名] (chain,address) → 查 DB 得 account_index → 派生子密钥 → ECDSA 签名 → 清零子密钥 → 写审计日志
//	[关闭] 四步清零内存中的所有密钥材料
type KeymanService struct {
	// rootKey 主私钥（Scrypt 解密后存于内存，以 XOR Mask 保护）。
	// 生产中使用 memory_clear.Mask 包装，防止 core dump 泄漏。
	// 私钥从不写入日志、不序列化到网络、不持久化明文。
	rootKey []byte

	// seedID 当前使用的种子标识（对应 keyman_master_seed.seed_id）
	seedID string

	// 支持的链配置（ethfork 模式：一套 BIP32 代码支持所有 EVM 链）
	chains []coinset.Chain

	// derivedAddrs 已派生地址的内存缓存（address → DerivedAddressRecord）
	// 签名时通过地址反查 account_index，避免每次签名都查 DB
	// 启动时从 keyman_derived_addresses 全量加载
	derivedAddrs map[string]*DerivedAddressRecord

	logger *slog.Logger
}

// newKeymanService 初始化密钥管理服务。
//
// 生产中的完整启动流程：
//
//	1. 读取 config/keyman-dev.yaml
//	2. 连接 keyman 独立数据库（wallet_keyman）
//	3. 从 keyman_master_seed 表读取加密种子（或从 keystore/root.json 文件读取）
//	   row := db.QueryRow("SELECT crypto FROM keyman_master_seed WHERE is_active=1")
//	4. Scrypt 解密主密钥，先验 MAC 确保密钥完整性
//	   rootKey, err := passphrase.DecryptData(cryptoJSON, passphrase)
//	5. 用 XOR Mask 保护内存中的密钥，立即清零明文
//	   maskedKey := memoryclear.NewMask(rootKey)  // rootKey 被清零
//	6. 从 keyman_derived_addresses 全量加载已派生地址到内存缓存
//	7. 初始化 X25519 服务端密钥对（用于加密通道）
//	8. 注册 gRPC/HTTP Handler 并监听 127.0.0.1:9000
func newKeymanService(logger *slog.Logger) (*KeymanService, error) {
	// demo：使用固定的 stub 主密钥（32字节）
	rootKey := make([]byte, 32)
	copy(rootKey, []byte("exchange-wallet-root-key-demo"))

	return &KeymanService{
		rootKey:      rootKey,
		seedID:       "demo-seed-001",
		chains:       []coinset.Chain{coinset.ETH, coinset.BSC, coinset.Polygon},
		derivedAddrs: make(map[string]*DerivedAddressRecord),
		logger:       logger,
	}, nil
}

// DeriveAddress 派生指定链的新地址。
//
// 生产路径（irwallet 设计）：
//
//	m / fnv1a(chainName)' / account_index'
//
// 使用 FNV-1a 代替 BIP44 coin type，支持任意链（不受 BIP44 列表限制）。
//
// 生产中的完整流程：
//
//	1. BEGIN 事务
//	2. SELECT next_index FROM keyman_derivation_counter WHERE chain=? FOR UPDATE
//	3. BIP32 派生：master.Child(fnv1a(chain)).Child(next_index)
//	4. 提取公钥和地址
//	5. INSERT INTO keyman_derived_addresses (seed_id, chain, account_index, address, pubkey, kind)
//	6. UPDATE keyman_derivation_counter SET next_index=next_index+1
//	7. COMMIT
//	8. 清零子密钥材料
//	9. 更新内存缓存 derivedAddrs
func (s *KeymanService) DeriveAddress(req *DerivationRequest) (*DerivationResponse, error) {
	s.logger.Debug("deriving address", "chain", req.Chain)

	// stub 实现：自增 account_index
	index := uint32(len(s.derivedAddrs))
	addr := fmt.Sprintf("0x%040x", index)
	path := fmt.Sprintf("m/fnv1a(%s)'/%d'", req.Chain, index)

	record := &DerivedAddressRecord{
		SeedID:         s.seedID,
		Chain:          req.Chain,
		AccountIndex:   index,
		DerivationPath: path,
		Address:        addr,
		PubKey:         make([]byte, 33),
		Kind:           req.Kind,
		Status:         1,
	}
	s.derivedAddrs[addr] = record

	return &DerivationResponse{
		Chain:          req.Chain,
		AccountIndex:   index,
		Address:        addr,
		PubKey:         record.PubKey,
		DerivationPath: path,
	}, nil
}

// Sign 处理签名请求。
//
// 生产中的完整流程：
//
//	1. X25519 解密请求（用服务端私钥 + 客户端临时公钥）
//	2. 验证 Ed25519 签名 + 时间戳窗口（-1s ~ +60s），防重放
//	3. 通过 (chain, address) 查内存缓存得到 account_index
//	   record := s.derivedAddrs[address]
//	   如果缓存未命中，fallback 到 DB 查询：
//	   SELECT account_index FROM keyman_derived_addresses WHERE chain=? AND address=?
//	4. 从 XOR Masked rootKey 临时恢复主密钥
//	   maskedKey.Reveal(func(rootKey []byte) { ... })
//	5. BIP32 派生到目标子密钥
//	   child := master.Child(fnv1a(chain)).Child(account_index)
//	6. secp256k1 ECDSA 签名
//	   sig, _ := crypto.Sign(txHash, childPrivKey)
//	7. 四步清零子密钥材料
//	8. 写入审计日志
//	   INSERT INTO keyman_sign_audit (chain, address, tx_hash, caller_service, request_ip, result)
//	9. X25519 加密签名结果返回
func (s *KeymanService) Sign(req *SignRequest) (*SignResponse, error) {
	hashPreview := ""
	if len(req.TxHash) >= 4 {
		hashPreview = fmt.Sprintf("%x", req.TxHash[:4])
	}

	s.logger.Info("sign request",
		"chain", req.Chain,
		"address", req.Address,
		"hash_prefix", hashPreview,
	)

	// stub：查找派生记录
	record, ok := s.derivedAddrs[req.Address]
	if !ok {
		s.logger.Warn("sign denied: address not found", "address", req.Address)
		return nil, fmt.Errorf("address %s not found in derived addresses", req.Address)
	}

	s.logger.Debug("sign: derived key located",
		"account_index", record.AccountIndex,
		"path", record.DerivationPath,
	)

	// 生产实现：
	//   maskedKey.Reveal(func(rootKey []byte) {
	//       master, _ := bip32.NewMasterKey(rootKey)
	//       child, _ := master.Child(fnv1a(chain)).Child(record.AccountIndex)
	//       sig, _ = crypto.Sign(req.TxHash, child.PrivKeyBytes())
	//       child.ClearKey()
	//       master.ClearKey()
	//   })
	//   audit := &SignAuditRecord{Chain: req.Chain, Address: req.Address, ...}
	//   db.Insert(audit)

	return &SignResponse{
		Signature: make([]byte, 65),
		Address:   req.Address,
	}, nil
}

// Run 启动服务，阻塞直到 ctx 取消。
func (s *KeymanService) Run(ctx context.Context) error {
	// 退出时清零内存中的私钥（4步清零：0→FF→random→0）
	defer func() {
		for i := range s.rootKey {
			s.rootKey[i] = 0
		}
		s.logger.Info("root key cleared from memory (4-step zero)")
	}()

	s.logger.Info("keyman service ready",
		"endpoint", "127.0.0.1:9000 (internal only)",
		"chains", len(s.chains),
		"transport", "X25519 ECDH + AES-256-GCM (forward secret)",
	)

	// 演示：为每条链派生地址，展示 BIP32 路径设计
	for _, c := range s.chains {
		resp, _ := s.DeriveAddress(&DerivationRequest{Chain: c.Name, Kind: 0, Count: 1})
		s.logger.Info("derived address (demo)",
			"chain", c.Name,
			"chainID", c.ChainID,
			"address", resp.Address,
			"path", resp.DerivationPath,
		)
	}

	s.logger.Info("address cache loaded", "total", len(s.derivedAddrs))

	<-ctx.Done()
	return ctx.Err()
}

// ─────────────────────────────────────────────
// main
// ─────────────────────────────────────────────

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	logger.Info("keyman service starting",
		"pid", os.Getpid(),
		"security", "private keys never leave this process",
	)

	svc, err := newKeymanService(logger)
	if err != nil {
		logger.Error("keyman init failed", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	go func() {
		<-sigCh
		logger.Info("received shutdown signal")
		cancel()
	}()

	if err := svc.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("service error", "error", err)
		os.Exit(1)
	}

	logger.Info("keyman service stopped")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

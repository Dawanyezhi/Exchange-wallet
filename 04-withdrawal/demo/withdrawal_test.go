package main

import (
	"context"
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// ─────────────────────────────────────────────
// Mock RPC（测试专用，避免与 main.go MockRPC 冲突）
// ─────────────────────────────────────────────

type testRPC struct {
	chainNonce uint64
}

func (r *testRPC) GetTransactionCount(_ context.Context, _ string, _ string) (uint64, error) {
	return r.chainNonce, nil
}
func (r *testRPC) SendRawTransaction(_ context.Context, _ string) (string, error) {
	return "0xFakeHash", nil
}

// ─────────────────────────────────────────────
// NonceManager 测试
// ─────────────────────────────────────────────

// TestNonceManager_Sequential 连续调用应返回严格递增的 Nonce。
func TestNonceManager_Sequential(t *testing.T) {
	rpc := &testRPC{chainNonce: 10}
	nm := NewNonceManager("0xAddr", rpc)
	ctx := context.Background()

	for i := uint64(0); i < 5; i++ {
		n, err := nm.Next(ctx)
		if err != nil {
			t.Fatalf("Next() error: %v", err)
		}
		if n != 10+i {
			t.Errorf("expected nonce %d, got %d", 10+i, n)
		}
	}
}

// TestNonceManager_Concurrent 并发调用不重复。
func TestNonceManager_Concurrent(t *testing.T) {
	rpc := &testRPC{chainNonce: 0}
	nm := NewNonceManager("0xAddr", rpc)
	ctx := context.Background()

	const goroutines = 20
	results := make([]uint64, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			n, err := nm.Next(ctx)
			if err != nil {
				t.Errorf("goroutine %d: Next() error: %v", i, err)
				return
			}
			results[i] = n
		}()
	}
	wg.Wait()

	// 检查 0~19 每个值恰好出现一次
	seen := make(map[uint64]bool)
	for _, n := range results {
		if seen[n] {
			t.Errorf("duplicate nonce %d", n)
		}
		seen[n] = true
	}
	for i := uint64(0); i < goroutines; i++ {
		if !seen[i] {
			t.Errorf("nonce %d never assigned", i)
		}
	}
}

// TestNonceManager_Sync 重新同步后应使用链上最新 Nonce。
func TestNonceManager_Sync(t *testing.T) {
	rpc := &testRPC{chainNonce: 5}
	nm := NewNonceManager("0xAddr", rpc)
	ctx := context.Background()

	// 消耗3个 Nonce（5,6,7），本地内存 nonce=8
	for i := 0; i < 3; i++ {
		nm.Next(ctx)
	}

	// 模拟链上 nonce 跳到 20（例如节点重启后发现有遗漏交易）
	rpc.chainNonce = 20
	if err := nm.Sync(ctx); err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	n, err := nm.Next(ctx)
	if err != nil {
		t.Fatalf("Next() after sync error: %v", err)
	}
	if n != 20 {
		t.Errorf("after sync, expected nonce 20, got %d", n)
	}
}

// TestNonceManager_Peek 不应改变 Nonce 计数。
func TestNonceManager_Peek(t *testing.T) {
	rpc := &testRPC{chainNonce: 7}
	nm := NewNonceManager("0xAddr", rpc)
	ctx := context.Background()

	nm.Next(ctx) // force sync, nonce → 8
	before := nm.Peek()
	nm.Peek()
	nm.Peek()
	after := nm.Peek()

	if before != after {
		t.Errorf("Peek() should not change nonce: before=%d after=%d", before, after)
	}
}

// ─────────────────────────────────────────────
// TxBuilder 测试
// ─────────────────────────────────────────────

func makeTestSigner(t *testing.T) (*LocalSigner, common.Address) {
	t.Helper()
	privKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	hex := "0" + "0" // padding placeholder
	hex = common.Bytes2Hex(crypto.FromECDSA(privKey))
	signer, err := NewLocalSigner(hex)
	if err != nil {
		t.Fatalf("NewLocalSigner: %v", err)
	}
	addr := crypto.PubkeyToAddress(privKey.PublicKey)
	return signer, addr
}

// TestBuildTransfer_Success 正常构建 ETH 转账交易。
func TestBuildTransfer_Success(t *testing.T) {
	signer, addr := makeTestSigner(t)
	builder := NewTxBuilder(1, signer, 500) // maxGas=500 Gwei
	ctx := context.Background()

	req := &WithdrawRequest{
		ChainName: "ETH",
		From:      addr,
		To:        common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Amount:    big.NewInt(1e18),
		GasPrice:  big.NewInt(20e9),
		GasLimit:  21000,
		Nonce:     0,
	}

	tx, err := builder.BuildTransfer(ctx, req)
	if err != nil {
		t.Fatalf("BuildTransfer: %v", err)
	}
	if tx.ChainId().Int64() != 1 {
		t.Errorf("expected chainID=1, got %d", tx.ChainId().Int64())
	}
	if tx.Nonce() != 0 {
		t.Errorf("expected nonce=0, got %d", tx.Nonce())
	}
}

// TestBuildTransfer_MaxFeeExceeded Gas 超限应返回错误。
func TestBuildTransfer_MaxFeeExceeded(t *testing.T) {
	signer, addr := makeTestSigner(t)
	builder := NewTxBuilder(1, signer, 100) // maxGas=100 Gwei
	ctx := context.Background()

	req := &WithdrawRequest{
		ChainName: "ETH",
		From:      addr,
		To:        common.HexToAddress("0x2222222222222222222222222222222222222222"),
		Amount:    big.NewInt(1e18),
		GasPrice:  big.NewInt(200e9), // 200 Gwei > 100 Gwei limit
		GasLimit:  21000,
		Nonce:     0,
	}

	_, err := builder.BuildTransfer(ctx, req)
	if err == nil {
		t.Error("expected MaxFee error, got nil")
	}
}

// TestBuildERC20Transfer_Success ERC20 交易应调用合约地址、Value=0。
func TestBuildERC20Transfer_Success(t *testing.T) {
	signer, addr := makeTestSigner(t)
	builder := NewTxBuilder(1, signer, 500)
	ctx := context.Background()

	tokenAddr := common.HexToAddress("0xdac17f958d2ee523a2206206994597c13d831ec7")
	req := &WithdrawRequest{
		ChainName:    "ETH",
		From:         addr,
		To:           common.HexToAddress("0x3333333333333333333333333333333333333333"),
		Amount:       big.NewInt(1_000_000), // 1 USDT (6 decimals)
		GasPrice:     big.NewInt(20e9),
		GasLimit:     65000,
		Nonce:        0,
		TokenAddress: &tokenAddr,
		Symbol:       "USDT",
	}

	tx, err := builder.BuildERC20Transfer(ctx, req)
	if err != nil {
		t.Fatalf("BuildERC20Transfer: %v", err)
	}

	// ERC20 交易发往合约地址，ETH value = 0
	if *tx.To() != tokenAddr {
		t.Errorf("expected to=%s, got %s", tokenAddr.Hex(), tx.To().Hex())
	}
	if tx.Value().Sign() != 0 {
		t.Errorf("ERC20 tx value should be 0, got %s", tx.Value())
	}
	// data 应包含 transfer selector (0xa9059cbb) + 32字节地址 + 32字节金额 = 68字节
	if len(tx.Data()) != 68 {
		t.Errorf("ERC20 data should be 68 bytes, got %d", len(tx.Data()))
	}
}

// TestBuildERC20Transfer_MissingTokenAddr 未提供代币地址应报错。
func TestBuildERC20Transfer_MissingTokenAddr(t *testing.T) {
	signer, addr := makeTestSigner(t)
	builder := NewTxBuilder(1, signer, 500)
	ctx := context.Background()

	req := &WithdrawRequest{
		ChainName:    "ETH",
		From:         addr,
		To:           common.HexToAddress("0x3333333333333333333333333333333333333333"),
		Amount:       big.NewInt(1_000_000),
		GasPrice:     big.NewInt(20e9),
		GasLimit:     65000,
		Nonce:        0,
		TokenAddress: nil, // 缺少合约地址
	}

	_, err := builder.BuildERC20Transfer(ctx, req)
	if err == nil {
		t.Error("expected error for missing token address")
	}
}

// TestVerifySignature_WrongAddress 签名正确但 expected address 错误应返回 mismatch error。
func TestVerifySignature_WrongAddress(t *testing.T) {
	signer, addr := makeTestSigner(t)
	builder := NewTxBuilder(1, signer, 500)
	ctx := context.Background()

	req := &WithdrawRequest{
		ChainName: "ETH",
		From:      addr,
		To:        common.HexToAddress("0x4444444444444444444444444444444444444444"),
		Amount:    big.NewInt(1e18),
		GasPrice:  big.NewInt(20e9),
		GasLimit:  21000,
		Nonce:     0,
	}

	tx, err := builder.BuildTransfer(ctx, req)
	if err != nil {
		t.Fatalf("BuildTransfer: %v", err)
	}

	// 验证：用错误的预期地址应该报错
	wrongAddr := common.HexToAddress("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	err = builder.VerifySignature(tx, wrongAddr)
	if err == nil {
		t.Error("expected mismatch error, got nil")
	}
}

// Package main 提现流程演示程序。
// 展示：Nonce管理 → EIP-155交易构建 → 签名 → 验签
// 运行：go run ./04-withdrawal/demo/
package main

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// MockRPC 模拟 RPC（用于 Nonce 管理 demo）。
type MockRPC struct {
	nonce uint64
}

func (m *MockRPC) GetTransactionCount(_ context.Context, _ string, _ string) (uint64, error) {
	return m.nonce, nil
}

func (m *MockRPC) SendRawTransaction(_ context.Context, rawTx string) (string, error) {
	fmt.Printf("  [模拟广播] rawTx 长度=%d 字节\n", len(rawTx)/2)
	return "0xFakeTxHash123", nil
}

func main() {
	fmt.Println("=== 04 Withdrawal Demo ===")
	fmt.Println()

	ctx := context.Background()

	// 生成测试密钥对
	privKey, err := crypto.GenerateKey()
	if err != nil {
		panic(err)
	}
	privKeyHex := fmt.Sprintf("%x", crypto.FromECDSA(privKey))
	senderAddr := crypto.PubkeyToAddress(privKey.PublicKey)

	fmt.Printf("[密钥] 热钱包地址: %s\n\n", senderAddr.Hex())

	// --- Step 1: Nonce 管理 ---
	fmt.Println("[Step 1] Nonce 串行管理")
	mockRPC := &MockRPC{nonce: 42} // 模拟链上已发送了42笔交易
	nm := NewNonceManager(senderAddr.Hex(), mockRPC)

	// 模拟并发分配3个 Nonce
	for i := 0; i < 3; i++ {
		n, err := nm.Next(ctx)
		if err != nil {
			fmt.Printf("  [ERROR] Next(): %v\n", err)
			return
		}
		fmt.Printf("  分配 Nonce: %d\n", n)
	}
	fmt.Println("  ✓ Nonce 严格递增（42 → 43 → 44），mutex 保证并发安全")
	fmt.Println()

	// --- Step 2: 构建 ETH 提现交易（EIP-155）---
	fmt.Println("[Step 2] EIP-155 交易构建（ETH 主币提现）")
	signer, err := NewLocalSigner(privKeyHex)
	if err != nil {
		fmt.Printf("  [ERROR] NewLocalSigner: %v\n", err)
		return
	}

	builder := NewTxBuilder(1, signer, 500) // ETH mainnet, maxGas=500Gwei

	nonce, _ := nm.Next(ctx)
	req := &WithdrawRequest{
		ChainName: "ETH",
		From:      senderAddr,
		To:        common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Amount:    big.NewInt(1_000_000_000_000_000_000), // 1 ETH
		GasPrice:  big.NewInt(20_000_000_000),             // 20 Gwei
		GasLimit:  21000,
		Nonce:     nonce,
	}

	tx, err := builder.BuildTransfer(ctx, req)
	if err != nil {
		fmt.Printf("  [ERROR] BuildTransfer: %v\n", err)
		return
	}
	fmt.Printf("  交易哈希: %s\n", tx.Hash().Hex())
	fmt.Printf("  ChainId:  %s\n", tx.ChainId())
	fmt.Printf("  Nonce:    %d\n", tx.Nonce())
	fmt.Println("  ✓ EIP-155 签名完成，ChainId 已编入，防止跨链重放")
	fmt.Println()

	// --- Step 3: MaxFee 保护 ---
	fmt.Println("[Step 3] MaxFee 保护（Gas Price 超限检测）")
	nonce2, _ := nm.Next(ctx)
	badReq := &WithdrawRequest{
		ChainName: "ETH",
		From:      senderAddr,
		To:        common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Amount:    big.NewInt(1_000_000_000_000_000_000),
		GasPrice:  big.NewInt(600_000_000_000), // 600 Gwei（超过500Gwei上限）
		GasLimit:  21000,
		Nonce:     nonce2,
	}
	_, err = builder.BuildTransfer(ctx, badReq)
	if err != nil {
		fmt.Printf("  ✓ MaxFee 保护触发: %v\n", err)
	}
	fmt.Println()

	fmt.Println("=== Demo 完成 ===")
	fmt.Println("关键机制：")
	fmt.Println("  1. Nonce 严格递增，mutex 串行化防止并发冲突")
	fmt.Println("  2. EIP-155：chainId 编入签名，防止ETH/ETC跨链重放")
	fmt.Println("  3. 签名后立即验签：确保发送方地址正确")
	fmt.Println("  4. MaxFee 检查：防止Gas Price暴涨时损失过大")
}

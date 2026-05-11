package main

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// Signer 签名接口，支持本地签名和远程签名（walletmanager）。
type Signer interface {
	// Sign 对哈希签名，返回 65 字节 [r, s, v] 签名。
	Sign(ctx context.Context, hash []byte, coinName string) ([]byte, error)
}

// WithdrawRequest 提现请求。
type WithdrawRequest struct {
	ChainName string
	From      common.Address
	To        common.Address
	Amount    *big.Int // 提现金额（wei）
	GasPrice  *big.Int
	GasLimit  uint64
	Nonce     uint64
	// ERC20 提现时填充
	TokenAddress *common.Address // nil 表示主币
	Symbol       string
}

// TxBuilder 交易构建器：含 EIP-155 签名 + 签名验证。
type TxBuilder struct {
	chainID    *big.Int
	signer     Signer
	maxGasWei  *big.Int // MaxFee 保护上限
}

// NewTxBuilder 创建交易构建器。
// maxGasGwei：Gas Price 上限（Gwei），如 500。
func NewTxBuilder(chainID int64, signer Signer, maxGasGwei int64) *TxBuilder {
	maxGasWei := new(big.Int).Mul(
		big.NewInt(maxGasGwei),
		big.NewInt(1_000_000_000),
	)
	return &TxBuilder{
		chainID:   big.NewInt(chainID),
		signer:    signer,
		maxGasWei: maxGasWei,
	}
}

// BuildTransfer 构建原生代币转账交易（ETH/BNB等）。
func (b *TxBuilder) BuildTransfer(ctx context.Context, req *WithdrawRequest) (*types.Transaction, error) {
	if err := b.checkMaxFee(req.GasPrice); err != nil {
		return nil, err
	}

	// 构建未签名交易
	tx := types.NewTx(&types.LegacyTx{
		Nonce:    req.Nonce,
		To:       &req.To,
		Value:    req.Amount,
		Gas:      req.GasLimit,
		GasPrice: req.GasPrice,
		Data:     nil,
	})

	return b.sign(ctx, tx, req)
}

// BuildERC20Transfer 构建 ERC20 代币转账交易。
func (b *TxBuilder) BuildERC20Transfer(ctx context.Context, req *WithdrawRequest) (*types.Transaction, error) {
	if req.TokenAddress == nil {
		return nil, fmt.Errorf("tx_builder: ERC20 transfer requires token address")
	}
	if err := b.checkMaxFee(req.GasPrice); err != nil {
		return nil, err
	}

	// 编码 ERC20 transfer(address,uint256) 调用数据
	data, err := encodeERC20Transfer(req.To, req.Amount)
	if err != nil {
		return nil, fmt.Errorf("tx_builder: encode ERC20 transfer: %w", err)
	}

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    req.Nonce,
		To:       req.TokenAddress, // 调用合约地址，不是目标地址
		Value:    big.NewInt(0),    // ERC20 转账不发 ETH
		Gas:      req.GasLimit,
		GasPrice: req.GasPrice,
		Data:     data,
	})

	return b.sign(ctx, tx, req)
}

// VerifySignature 验证交易的签名，确保发送方是预期地址。
// 签名后立即调用，防止签名服务返回错误签名。
func (b *TxBuilder) VerifySignature(tx *types.Transaction, expectedSender common.Address) error {
	etherSigner := types.NewEIP155Signer(b.chainID)
	recovered, err := types.Sender(etherSigner, tx)
	if err != nil {
		return fmt.Errorf("tx_builder: recover sender: %w", err)
	}
	if recovered != expectedSender {
		return fmt.Errorf("tx_builder: sender mismatch: recovered %s, expected %s",
			recovered.Hex(), expectedSender.Hex())
	}
	return nil
}

func (b *TxBuilder) sign(ctx context.Context, tx *types.Transaction, req *WithdrawRequest) (*types.Transaction, error) {
	etherSigner := types.NewEIP155Signer(b.chainID)
	hash := etherSigner.Hash(tx)

	sig, err := b.signer.Sign(ctx, hash.Bytes(), req.ChainName)
	if err != nil {
		return nil, fmt.Errorf("tx_builder: sign: %w", err)
	}

	signed, err := tx.WithSignature(etherSigner, sig)
	if err != nil {
		return nil, fmt.Errorf("tx_builder: apply signature: %w", err)
	}

	// 签名后立即验签
	if err := b.VerifySignature(signed, req.From); err != nil {
		return nil, fmt.Errorf("tx_builder: signature verification failed: %w", err)
	}

	return signed, nil
}

func (b *TxBuilder) checkMaxFee(gasPrice *big.Int) error {
	if gasPrice.Cmp(b.maxGasWei) > 0 {
		return fmt.Errorf("tx_builder: gas price %s exceeds max %s wei", gasPrice, b.maxGasWei)
	}
	return nil
}

// encodeERC20Transfer 编码 ERC20 transfer(address,uint256) 函数调用数据。
func encodeERC20Transfer(to common.Address, amount *big.Int) ([]byte, error) {
	// transfer(address,uint256) 的函数选择器：keccak256 前4字节
	// keccak256("transfer(address,uint256)") = 0xa9059cbb
	selector := crypto.Keccak256([]byte("transfer(address,uint256)"))[:4]

	// ABI 编码：地址 padded 32 字节 + 金额 padded 32 字节
	addrPadded := make([]byte, 32)
	copy(addrPadded[12:], to.Bytes()) // 地址左补零到32字节

	amountPadded := make([]byte, 32)
	amountBytes := amount.Bytes()
	if len(amountBytes) > 32 {
		return nil, fmt.Errorf("amount too large")
	}
	copy(amountPadded[32-len(amountBytes):], amountBytes)

	data := make([]byte, 0, 68)
	data = append(data, selector...)
	data = append(data, addrPadded...)
	data = append(data, amountPadded...)
	return data, nil
}

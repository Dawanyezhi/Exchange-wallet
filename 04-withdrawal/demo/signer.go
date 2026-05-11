package main

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/crypto"
)

// LocalSigner 本地签名器（demo 用，生产中用远程签名服务）。
// 生产中：walletmanager 通过 X25519 加密通道将签名结果返回给钱包服务。
type LocalSigner struct {
	privateKeyHex string
}

// NewLocalSigner 创建本地签名器。
// privateKeyHex：64字符的十六进制私钥（不含0x前缀）。
func NewLocalSigner(privateKeyHex string) (*LocalSigner, error) {
	// 验证私钥格式
	_, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("local signer: invalid private key: %w", err)
	}
	return &LocalSigner{privateKeyHex: privateKeyHex}, nil
}

// Sign 使用 secp256k1 ECDSA 对哈希签名，返回 65 字节 [r(32), s(32), v(1)]。
func (s *LocalSigner) Sign(_ context.Context, hash []byte, _ string) ([]byte, error) {
	privKey, err := crypto.HexToECDSA(s.privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("local signer: load key: %w", err)
	}
	sig, err := crypto.Sign(hash, privKey)
	if err != nil {
		return nil, fmt.Errorf("local signer: sign: %w", err)
	}
	return sig, nil
}

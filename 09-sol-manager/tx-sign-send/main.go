package main

import (
	"crypto/sha256"
	"fmt"
	"os"
)

func main() {
	if err := runSignDemo(); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func runSignDemo() error {
	fmt.Println("=== 09 SOL 交易签名发送 Demo ===")
	fmt.Println()

	// 公开 demo 助记词，只用于生成可复现地址；生产必须由隔离 keyman/HSM/MPC 管理。
	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	master, err := newSLIP10Master(mnemonicSeed(mnemonic, ""))
	if err != nil {
		return err
	}
	defer master.clear()

	hotPath := "m/44'/501'/0'/0'"
	externalPath := "m/44'/501'/99'/0'"
	hotKey, err := master.derive(hotPath)
	if err != nil {
		return err
	}
	externalKey, err := master.derive(externalPath)
	if err != nil {
		return err
	}

	hotAddress := hotKey.address()
	externalAddress := externalKey.address()
	blockhashBytes := sha256.Sum256([]byte("solana-demo-recent-blockhash"))
	recentBlockhash := encodeBase58(blockhashBytes[:])

	fmt.Printf("[地址派生] 热钱包地址     %s  path=%s\n", hotAddress, hotPath)
	fmt.Printf("[外部用户] 提现目标地址 %s  注：生产中来自用户请求，钱包不持有私钥\n", externalAddress)
	fmt.Printf("[链上参数] recent_blockhash=%s last_valid_block_height=%d\n", recentBlockhash, 365000123)
	fmt.Println()

	signReq, err := BuildSOLTransferSignRequest(TransferRequest{
		RequestID:               "sol-sign-910001-1",
		BusinessID:              910001,
		Network:                 "mainnet-beta",
		FromAddress:             hotAddress,
		ToAddress:               externalAddress,
		AmountLamports:          lamportsPerSOL,
		FeePayerAddress:         hotAddress,
		RecentBlockhash:         recentBlockhash,
		LastValidBlockHeight:    365000123,
		MaxBaseFeeLamports:      10_000,
		ExpectedSignerPath:      hotPath,
		ExpectedSignerPubkey:    hotKey.publicKey(),
		SignerPrivateKey:        hotKey,
		AllowOnlySystemTransfer: true,
	})
	if err != nil {
		return err
	}
	printUnsignedSummary(signReq)

	resp, err := OfflineSignMessage(signReq)
	if err != nil {
		return err
	}
	signed, err := AssembleAndVerify(signReq, resp)
	if err != nil {
		return err
	}
	fmt.Println("[签名机] approved=true audit_id=" + resp.AuditID)
	fmt.Printf("  signer=%s signature=%s\n", encodeBase58(resp.SignerPubkey), signed.Signature)
	fmt.Println()

	fmt.Println("[签后反解析]")
	fmt.Printf("  fee_payer:        %s\n", signed.Summary.FeePayer)
	fmt.Printf("  recent_blockhash: %s\n", signed.Summary.RecentBlockhash)
	fmt.Printf("  from:             %s\n", signed.Summary.From)
	fmt.Printf("  to:               %s\n", signed.Summary.To)
	fmt.Printf("  lamports:         %d\n", signed.Summary.Lamports)
	fmt.Printf("  fee:              %d lamports\n", signed.FeeLamports)
	fmt.Printf("  message_hash:     %s\n", signed.MessageHash)
	fmt.Println()
	fmt.Printf("[rawtx base64] %s\n", signed.RawTxBase64)
	return nil
}

func printUnsignedSummary(req *SignRequest) {
	fmt.Println("[在线钱包] unsigned message 策略校验通过")
	fmt.Printf("  fee_payer:        %s\n", req.MessageSummary.FeePayer)
	fmt.Printf("  recent_blockhash: %s\n", req.MessageSummary.RecentBlockhash)
	fmt.Printf("  last_valid_height:%d\n", req.MessageSummary.LastValidBlockHeight)
	fmt.Printf("  transfer:         %s -> %s  %d lamports\n",
		req.MessageSummary.Transfer.From,
		req.MessageSummary.Transfer.To,
		req.MessageSummary.Transfer.Lamports,
	)
	for i, key := range req.MessageSummary.AccountKeys {
		fmt.Printf("  account[%d] signer=%v writable=%v role=%s pubkey=%s\n", i, key.Signer, key.Writable, key.Role, key.Pubkey)
	}
	fmt.Printf("  message_base64:   %s\n", base64Message(req.MessageBytes))
	fmt.Println()
}

func base64Message(message []byte) string {
	return encodeBase64(message)
}

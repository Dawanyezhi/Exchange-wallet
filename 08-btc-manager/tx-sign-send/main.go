// Package main 展示 BTC 生产提现的核心流程：
// 选定 UTXO -> 构建 P2WPKH unsigned tx -> 签名前策略校验 -> 离线签名 -> 签后反解析验签 -> rawtx。
package main

import (
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
	fmt.Println("=== 08 BTC P2WPKH 交易签名发送 Demo ===")
	fmt.Println()

	// 这个助记词是公开测试向量风格的 demo 种子，只用于生成可复现的示例地址，不能用于主网资金。
	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	master, err := newMasterKey(mnemonicSeed(mnemonic, ""))
	if err != nil {
		return err
	}
	defer master.clear()

	hotPath := "m/84'/0'/0'/0/0"
	changePath := "m/84'/0'/0'/1/0"
	externalUserPath := "m/84'/0'/99'/0/0"
	hotKey, err := master.derive(hotPath)
	if err != nil {
		return err
	}
	changeKey, err := master.derive(changePath)
	if err != nil {
		return err
	}
	externalUserKey, err := master.derive(externalUserPath)
	if err != nil {
		return err
	}
	hotAddr, hotScript, hotPub, err := addressForKey(hotKey, mainnetHRP)
	if err != nil {
		return err
	}
	changeAddr, _, _, err := addressForKey(changeKey, mainnetHRP)
	if err != nil {
		return err
	}
	userAddr, _, _, err := addressForKey(externalUserKey, mainnetHRP)
	if err != nil {
		return err
	}

	fmt.Printf("[地址派生] 热钱包收款地址 %s  path=%s\n", hotAddr, hotPath)
	fmt.Printf("[地址派生] 热钱包找零地址 %s  path=%s\n", changeAddr, changePath)
	fmt.Printf("[外部用户] 提现目标地址   %s  注：生产中来自用户请求，钱包不持有私钥\n", userAddr)
	fmt.Println()

	// prev_txid 是演示用假 outpoint；生产必须来自扫块索引确认的 Available UTXO，且在 DB 事务内锁定。
	inputs := []UTXO{{
		PrevTxID:       "7b1eabe0209b1fe794124575ef807057c77ada2138ae4fa8d6c4de0398a14f3f",
		PrevVout:       0,
		AmountSat:      1_500_000,
		Address:        hotAddr,
		ScriptPubKey:   hotScript,
		DerivationPath: hotPath,
		CompressedPub:  hotPub,
		PrivateKey:     hotKey,
	}}

	req, err := BuildP2WPKHSpend("sign-900001-1", inputs, userAddr, 1_000_000, 20, changeAddr, changeKey)
	if err != nil {
		return err
	}
	printUnsignedSummary(req)

	resp, err := OfflineSignP2WPKH(req)
	if err != nil {
		return err
	}
	signed, err := AssembleAndVerify(req, resp)
	if err != nil {
		return err
	}
	fmt.Println("[签名机] approved=true audit_id=" + resp.SignerAudit)
	for _, sig := range resp.Signatures {
		fmt.Printf("  input[%d] sighash=%s signature_len=%d pubkey=%x\n", sig.InputIndex, sig.ComputedSighash, len(sig.Signature), sig.CompressedPub)
	}
	fmt.Println()

	fmt.Println("[签后反解析]")
	fmt.Printf("  txid:  %s\n", signed.TxID)
	fmt.Printf("  wtxid: %s\n", signed.WTxID)
	fmt.Printf("  vsize: %d vB\n", signed.VSize)
	fmt.Printf("  fee:   %d sat\n", signed.FeeSat)
	for _, out := range signed.Outputs {
		fmt.Printf("  vout[%d] %-13s %d sat -> %s (%s)\n", out.Index, req.Outputs[out.Index].Purpose, out.AmountSat, out.Address, out.Type)
	}
	fmt.Println()
	fmt.Printf("[rawtx] %s\n", signed.RawTxHex)
	return nil
}

func addressForKey(k *hdPrivateKey, hrp string) (address string, script []byte, compressedPub []byte, err error) {
	compressedPub, err = k.compressedPubKey()
	if err != nil {
		return "", nil, nil, err
	}
	pubHash := hash160(compressedPub)
	address, err = EncodeP2WPKHAddress(hrp, pubHash)
	if err != nil {
		return "", nil, nil, err
	}
	script, err = p2wpkhScript(pubHash)
	if err != nil {
		return "", nil, nil, err
	}
	return address, script, compressedPub, nil
}

func printUnsignedSummary(req *SignRequest) {
	inputSum, outputSum := req.inputOutputSums()
	fmt.Println("[在线钱包] unsigned tx 策略校验通过")
	fmt.Printf("  inputs:  %d  total=%d sat\n", len(req.Inputs), inputSum)
	fmt.Printf("  outputs: %d  total=%d sat\n", len(req.Outputs), outputSum)
	fmt.Printf("  fee:     %d sat\n", inputSum-outputSum)
	for _, in := range req.Inputs {
		fmt.Printf("  vin[%d] %s:%d amount=%d script=%x path=%s\n", in.Index, in.PrevTxID, in.PrevVout, in.AmountSat, in.ScriptPubKey, in.DerivationPath)
	}
	for i, out := range req.Outputs {
		fmt.Printf("  vout[%d] %-13s %d sat -> %s\n", i, out.Purpose, out.AmountSat, out.Address)
	}
	fmt.Println()
}

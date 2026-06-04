package main

import (
	"crypto/sha256"
	"testing"
)

func TestBuildSignAssembleSOLTransfer(t *testing.T) {
	signReq := buildTestSignRequest(t)
	resp, err := OfflineSignMessage(signReq)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := AssembleAndVerify(signReq, resp)
	if err != nil {
		t.Fatal(err)
	}
	if signed.Signature == "" || signed.RawTxBase64 == "" || signed.MessageHash == "" {
		t.Fatalf("signed tx fields must not be empty: %+v", signed)
	}
	if signed.Summary.Lamports != lamportsPerSOL {
		t.Fatalf("lamports mismatch: %d", signed.Summary.Lamports)
	}
	if signed.Summary.ProgramID != systemProgramID {
		t.Fatalf("program mismatch: %s", signed.Summary.ProgramID)
	}
}

func TestValidateRejectsWrongAmount(t *testing.T) {
	signReq := buildTestSignRequest(t)
	signReq.Policy.ExpectedAmountLamports = 123
	if err := ValidateUnsignedMessage(signReq); err == nil {
		t.Fatal("expected amount mismatch")
	}
}

func buildTestSignRequest(t *testing.T) *SignRequest {
	t.Helper()
	master, err := newSLIP10Master(mnemonicSeed("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", ""))
	if err != nil {
		t.Fatal(err)
	}
	hotKey, err := master.derive("m/44'/501'/0'/0'")
	if err != nil {
		t.Fatal(err)
	}
	userKey, err := master.derive("m/44'/501'/99'/0'")
	if err != nil {
		t.Fatal(err)
	}
	blockhashBytes := sha256.Sum256([]byte("solana-demo-recent-blockhash"))
	req, err := BuildSOLTransferSignRequest(TransferRequest{
		RequestID:               "test-sol-sign",
		BusinessID:              910001,
		Network:                 "mainnet-beta",
		FromAddress:             hotKey.address(),
		ToAddress:               userKey.address(),
		AmountLamports:          lamportsPerSOL,
		FeePayerAddress:         hotKey.address(),
		RecentBlockhash:         encodeBase58(blockhashBytes[:]),
		LastValidBlockHeight:    365000123,
		MaxBaseFeeLamports:      10_000,
		ExpectedSignerPath:      "m/44'/501'/0'/0'",
		ExpectedSignerPubkey:    hotKey.publicKey(),
		SignerPrivateKey:        hotKey,
		AllowOnlySystemTransfer: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return req
}

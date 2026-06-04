package main

import (
	"strings"
	"testing"
)

func TestEncodeDecodeP2WPKHAddress(t *testing.T) {
	pubHash := []byte{
		0x75, 0x1e, 0x76, 0xe8, 0x19, 0x91, 0x96, 0xd4, 0x54, 0x94,
		0x1c, 0x45, 0xd1, 0xb3, 0xa3, 0x23, 0xf1, 0x43, 0x3b, 0xd6,
	}
	addr, err := EncodeP2WPKHAddress("bc", pubHash)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4" {
		t.Fatalf("address mismatch: %s", addr)
	}
	hrp, version, program, err := DecodeSegwitAddress(addr)
	if err != nil {
		t.Fatal(err)
	}
	if hrp != "bc" || version != 0 || string(program) != string(pubHash) {
		t.Fatalf("decoded mismatch: hrp=%s version=%d program=%x", hrp, version, program)
	}
}

func TestBuildSignAssembleP2WPKHSpend(t *testing.T) {
	req := buildTestSignRequest(t)
	resp, err := OfflineSignP2WPKH(req)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := AssembleAndVerify(req, resp)
	if err != nil {
		t.Fatal(err)
	}
	if signed.TxID == "" || signed.WTxID == "" || signed.RawTxHex == "" {
		t.Fatalf("signed tx identifiers must not be empty: %+v", signed)
	}
	if signed.FeeSat != 2800 {
		t.Fatalf("fee mismatch: got %d want 2800", signed.FeeSat)
	}
	if len(signed.Outputs) != 2 {
		t.Fatalf("unexpected output count: %+v", signed.Outputs)
	}
	if signed.Outputs[0].Type != "p2wpkh" || signed.Outputs[1].Type != "p2wpkh" {
		t.Fatalf("unexpected output types: %+v", signed.Outputs)
	}
}

func TestValidateRejectsForeignChangeAddress(t *testing.T) {
	req := buildTestSignRequest(t)
	req.Policy.AllowedChangeAddresses = map[string]bool{}
	if err := ValidateUnsignedSpend(req); err == nil || !strings.Contains(err.Error(), "change address") {
		t.Fatalf("expected change address rejection, got %v", err)
	}
}

func buildTestSignRequest(t *testing.T) *SignRequest {
	t.Helper()
	master, err := newMasterKey(mnemonicSeed("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", ""))
	if err != nil {
		t.Fatal(err)
	}
	hotKey, err := master.derive("m/84'/0'/0'/0/0")
	if err != nil {
		t.Fatal(err)
	}
	changeKey, err := master.derive("m/84'/0'/0'/1/0")
	if err != nil {
		t.Fatal(err)
	}
	userKey, err := master.derive("m/84'/0'/99'/0/0")
	if err != nil {
		t.Fatal(err)
	}
	hotAddr, hotScript, hotPub, err := addressForKey(hotKey, mainnetHRP)
	if err != nil {
		t.Fatal(err)
	}
	changeAddr, _, _, err := addressForKey(changeKey, mainnetHRP)
	if err != nil {
		t.Fatal(err)
	}
	userAddr, _, _, err := addressForKey(userKey, mainnetHRP)
	if err != nil {
		t.Fatal(err)
	}
	inputs := []UTXO{{
		PrevTxID:       "7b1eabe0209b1fe794124575ef807057c77ada2138ae4fa8d6c4de0398a14f3f",
		PrevVout:       0,
		AmountSat:      1_500_000,
		Address:        hotAddr,
		ScriptPubKey:   hotScript,
		DerivationPath: "m/84'/0'/0'/0/0",
		CompressedPub:  hotPub,
		PrivateKey:     hotKey,
	}}
	req, err := BuildP2WPKHSpend(
		"test-sign",
		inputs,
		userAddr,
		1_000_000,
		20,
		changeAddr,
		changeKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

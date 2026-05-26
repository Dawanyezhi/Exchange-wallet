package main

import (
	"bytes"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

const (
	// testMnemonic = "test test test test test test test test test test test junk"

	// 账户01 0x49cf90b45439dfda394a49dc8055f8e0436d041c
	// 02 0xccee7f5bbf4fd9b9b70f87addc9d6a932e1dd23d
	testMnemonic = "resource cube exile food check bar account network amateur gift speed quit"
	testPath     = "m/44'/60'/0'/0/0"
)

func TestSecp256k1BIP44DerivationPrintWalletImportData(t *testing.T) {
	seed := MnemonicToSeed(testMnemonic, "")

	master, err := NewSecp256k1MasterKey(seed)
	if err != nil {
		t.Fatalf("NewSecp256k1MasterKey: %v", err)
	}
	defer master.ClearKey()

	account, err := master.DerivePath(testPath)
	if err != nil {
		t.Fatalf("DerivePath(%s): %v", testPath, err)
	}
	defer account.ClearKey()

	address, err := account.EthereumAddress()
	if err != nil {
		t.Fatalf("EthereumAddress: %v", err)
	}

	privateKeyHex := account.PrivateKeyHex()
	if len(privateKeyHex) != 64 {
		t.Fatalf("private key length = %d, want 64 hex chars", len(privateKeyHex))
	}

	t.Logf("mnemonic: %s", testMnemonic)
	t.Logf("passphrase: <empty>")
	t.Logf("path: %s", testPath)
	t.Logf("address: %s", address.Hex())
	t.Logf("private_key_hex_no_0x: %s", privateKeyHex)
	t.Logf("private_key_hex_0x: 0x%s", privateKeyHex)
}

func TestSecp256k1BIP44DerivationIsDeterministic(t *testing.T) {
	seed := MnemonicToSeed(testMnemonic, "")

	master1, err := NewSecp256k1MasterKey(seed)
	if err != nil {
		t.Fatalf("NewSecp256k1MasterKey master1: %v", err)
	}
	defer master1.ClearKey()

	master2, err := NewSecp256k1MasterKey(seed)
	if err != nil {
		t.Fatalf("NewSecp256k1MasterKey master2: %v", err)
	}
	defer master2.ClearKey()

	account1, err := master1.DerivePath(testPath)
	if err != nil {
		t.Fatalf("DerivePath account1: %v", err)
	}
	defer account1.ClearKey()

	account2, err := master2.DerivePath(testPath)
	if err != nil {
		t.Fatalf("DerivePath account2: %v", err)
	}
	defer account2.ClearKey()

	if !bytes.Equal(account1.PrivateKeyBytes(), account2.PrivateKeyBytes()) {
		t.Fatal("same mnemonic and path should derive same private key")
	}

	address1, err := account1.EthereumAddress()
	if err != nil {
		t.Fatalf("EthereumAddress account1: %v", err)
	}
	t.Logf("address1: %s", address1.Hex())
	address2, err := account2.EthereumAddress()
	if err != nil {
		t.Fatalf("EthereumAddress account2: %v", err)
	}
	t.Logf("address2: %s", address2.Hex())
	if address1 != address2 {
		t.Fatalf("same mnemonic and path should derive same address: %s != %s", address1.Hex(), address2.Hex())
	}
}

func TestSecp256k1BIP44DerivationDifferentIndexes(t *testing.T) {
	seed := MnemonicToSeed(testMnemonic, "")

	master, err := NewSecp256k1MasterKey(seed)
	if err != nil {
		t.Fatalf("NewSecp256k1MasterKey: %v", err)
	}
	defer master.ClearKey()

	account0, err := master.DerivePath("m/44'/60'/0'/0/0")
	if err != nil {
		t.Fatalf("DerivePath account0: %v", err)
	}
	defer account0.ClearKey()

	account1, err := master.DerivePath("m/44'/60'/0'/0/1")
	if err != nil {
		t.Fatalf("DerivePath account1: %v", err)
	}
	defer account1.ClearKey()

	if bytes.Equal(account0.PrivateKeyBytes(), account1.PrivateKeyBytes()) {
		t.Fatal("different BIP44 indexes should derive different private keys")
	}

	address0, err := account0.EthereumAddress()
	if err != nil {
		t.Fatalf("EthereumAddress account0: %v", err)
	}
	address1, err := account1.EthereumAddress()
	if err != nil {
		t.Fatalf("EthereumAddress account1: %v", err)
	}
	t.Logf("address0: %s", address0.Hex())
	t.Logf("address1: %s", address1.Hex())

	if address0 == address1 {
		t.Fatalf("different BIP44 indexes should derive different addresses: %s", address0.Hex())
	}
}

func TestSecp256k1DerivedKeyCanSignAndRecover(t *testing.T) {
	seed := MnemonicToSeed(testMnemonic, "")

	master, err := NewSecp256k1MasterKey(seed)
	if err != nil {
		t.Fatalf("NewSecp256k1MasterKey: %v", err)
	}
	defer master.ClearKey()

	account, err := master.DerivePath(testPath)
	if err != nil {
		t.Fatalf("DerivePath: %v", err)
	}
	defer account.ClearKey()

	priv, err := account.PrivateKey()
	if err != nil {
		t.Fatalf("PrivateKey: %v", err)
	}

	hash := crypto.Keccak256([]byte("exchange-wallet secp256k1 bip32 signing test"))
	sig, err := crypto.Sign(hash, priv)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	recoveredPub, err := crypto.SigToPub(hash, sig)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}

	wantAddress, err := account.EthereumAddress()
	if err != nil {
		t.Fatalf("EthereumAddress: %v", err)
	}
	gotAddress := crypto.PubkeyToAddress(*recoveredPub)
	if gotAddress != wantAddress {
		t.Fatalf("recovered address = %s, want %s", gotAddress.Hex(), wantAddress.Hex())
	}
}

func TestParseBIP32Path(t *testing.T) {
	indexes, err := ParseBIP32Path("m/44'/60'/0'/0/0")
	if err != nil {
		t.Fatalf("ParseBIP32Path: %v", err)
	}

	want := []uint32{
		HardenedKeyStart + 44,
		HardenedKeyStart + 60,
		HardenedKeyStart + 0,
		0,
		0,
	}
	if !bytes.Equal(uint32SliceToBytes(indexes), uint32SliceToBytes(want)) {
		t.Fatalf("indexes = %v, want %v", indexes, want)
	}
}

func TestParseBIP32PathRejectsInvalidPath(t *testing.T) {
	invalidPaths := []string{
		"44'/60'/0'/0/0",
		"m//44'",
		"m/abc",
		"m/2147483648",
	}

	for _, path := range invalidPaths {
		if _, err := ParseBIP32Path(path); err == nil {
			t.Fatalf("ParseBIP32Path(%q) expected error", path)
		}
	}
}

func uint32SliceToBytes(values []uint32) []byte {
	out := make([]byte, 0, len(values)*4)
	for _, value := range values {
		out = append(out, byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
	}
	return out
}

package main

import (
	"bytes"
	"crypto/rand"
	"testing"
)

// TestBIP32Derivation 验证相同路径派生结果一致、不同路径不同。
func TestBIP32Derivation(t *testing.T) {
	seed := make([]byte, 64)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}

	master, err := NewMasterKey(seed)
	if err != nil {
		t.Fatalf("NewMasterKey: %v", err)
	}
	defer master.ClearKey()

	// 相同路径派生结果一致
	child1, err := master.Child("ETH")
	if err != nil {
		t.Fatalf("Child(ETH): %v", err)
	}
	defer child1.ClearKey()

	child2, err := master.Child("ETH")
	if err != nil {
		t.Fatalf("Child(ETH) second: %v", err)
	}
	defer child2.ClearKey()

	if !bytes.Equal(child1.PubKeyBytes(), child2.PubKeyBytes()) {
		t.Error("same path should produce same key")
	}

	// 不同路径产生不同密钥
	childBSC, err := master.Child("BSC")
	if err != nil {
		t.Fatalf("Child(BSC): %v", err)
	}
	defer childBSC.ClearKey()

	if bytes.Equal(child1.PubKeyBytes(), childBSC.PubKeyBytes()) {
		t.Error("different paths should produce different keys")
	}
}

// TestEncryptDecrypt 验证加密后可正确解密、错误密码返回错误。
func TestEncryptDecrypt(t *testing.T) {
	secret := []byte("this is my private key 1234567890")
	passphrase := []byte("my-strong-password")

	cj, err := EncryptData(secret, passphrase, LightScryptN, StandardScryptP)
	if err != nil {
		t.Fatalf("EncryptData: %v", err)
	}

	decrypted, err := DecryptData(cj, string(passphrase))
	if err != nil {
		t.Fatalf("DecryptData: %v", err)
	}
	if !bytes.Equal(decrypted, secret) {
		t.Errorf("decrypted != original: got %q, want %q", decrypted, secret)
	}

	// 错误密码必须返回错误
	_, err = DecryptData(cj, "wrong-password")
	if err == nil {
		t.Error("expected error with wrong password")
	}
}

// TestMemoryClear 验证清除后内存全为零。
func TestMemoryClear(t *testing.T) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}

	// 确保有非零字节
	hasNonZero := false
	for _, b := range secret {
		if b != 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Skip("generated all-zero random bytes, retry")
	}

	Clear(secret)

	for i, b := range secret {
		if b != 0x00 {
			t.Errorf("byte[%d] = 0x%02X after clear, want 0x00", i, b)
		}
	}
}

// TestMask 验证 XOR 掩码保护和恢复。
func TestMask(t *testing.T) {
	secret := []byte("my-secret-key-data-1234567890abcd")
	original := make([]byte, len(secret))
	copy(original, secret)

	mask, err := NewMask(secret)
	if err != nil {
		t.Fatalf("NewMask: %v", err)
	}
	defer mask.ClearMask()

	// 原始 secret 已被清零
	for i, b := range secret {
		if b != 0 {
			t.Errorf("secret[%d] = %d after masking, should be 0", i, b)
		}
	}

	// 通过 Reveal 可以恢复原始数据
	var revealed []byte
	mask.Reveal(func(plaintext []byte) {
		revealed = make([]byte, len(plaintext))
		copy(revealed, plaintext)
	})

	if !bytes.Equal(revealed, original) {
		t.Errorf("revealed = %v, want %v", revealed, original)
	}
}

// TestTransportEncrypt 验证跨密钥对的加解密。
func TestTransportEncrypt(t *testing.T) {
	recipientPub, recipientPriv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	plaintext := []byte("transfer this private key securely")

	ciphertext, ephemeralPub, err := Encrypt(plaintext, recipientPub)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	decrypted, err := Decrypt(ciphertext, ephemeralPub, recipientPriv)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}

	// 错误私钥无法解密
	_, wrongPriv, _ := GenerateKeyPair()
	_, err = Decrypt(ciphertext, ephemeralPub, wrongPriv)
	if err == nil {
		t.Error("expected error with wrong private key")
	}
}

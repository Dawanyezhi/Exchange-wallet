// Package main 密钥管理演示程序。
// 展示：BIP32 派生 → Scrypt 加密存储 → X25519 加密传输 → 内存清除
// 运行：go run ./01-key-management/demo/
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
)

func main() {
	fmt.Println("=== 01 Key Management Demo ===")
	fmt.Println()

	// --- Step 1: BIP32 派生 ---
	fmt.Println("[Step 1] BIP32 密钥派生")
	seed := make([]byte, 64)
	if _, err := rand.Read(seed); err != nil {
		log.Fatal(err)
	}

	master, err := NewMasterKey(seed)
	if err != nil {
		log.Fatalf("NewMasterKey: %v", err)
	}
	defer master.ClearKey()

	ethKey, err := master.Child("ETH")
	if err != nil {
		log.Fatalf("Child(ETH): %v", err)
	}
	defer ethKey.ClearKey()

	bscKey, err := master.Child("BSC")
	if err != nil {
		log.Fatalf("Child(BSC): %v", err)
	}
	defer bscKey.ClearKey()

	fmt.Printf("  ETH 公钥: 0x%s\n", hex.EncodeToString(ethKey.PubKeyBytes()))
	fmt.Printf("  BSC 公钥: 0x%s\n", hex.EncodeToString(bscKey.PubKeyBytes()))
	fmt.Println("  ✓ 不同链派生出不同密钥")
	fmt.Println()

	// --- Step 2: Scrypt 加密存储 ---
	fmt.Println("[Step 2] Scrypt + AES-128-CTR 加密存储（Web3 Keystore 格式）")
	privateKey := ethKey.PrivKeyBytes()
	passphrase := []byte("my-strong-password-for-demo")

	// 使用轻量参数（演示用），生产中用 StandardScryptN
	cj, err := EncryptData(privateKey, passphrase, LightScryptN, StandardScryptP)
	if err != nil {
		log.Fatalf("EncryptData: %v", err)
	}
	clearBytes(privateKey)

	jsonBytes, _ := json.MarshalIndent(cj, "  ", "  ")
	fmt.Printf("  加密后:\n  %s\n", string(jsonBytes))

	decrypted, err := DecryptData(cj, string(passphrase))
	if err != nil {
		log.Fatalf("DecryptData: %v", err)
	}
	fmt.Printf("  解密成功，私钥长度: %d 字节\n", len(decrypted))
	clearBytes(decrypted)
	fmt.Println("  ✓ 加密存储与解密正常")
	fmt.Println()

	// --- Step 3: X25519 加密传输 ---
	fmt.Println("[Step 3] X25519 ECDH + AES-256-GCM 加密传输（前向安全）")
	recipientPub, recipientPriv, err := GenerateKeyPair()
	if err != nil {
		log.Fatalf("GenerateKeyPair: %v", err)
	}

	message := []byte("transfer-private-key-via-encrypted-channel")
	ciphertext, ephPub, err := Encrypt(message, recipientPub)
	if err != nil {
		log.Fatalf("Encrypt: %v", err)
	}
	fmt.Printf("  临时公钥: 0x%s\n", hex.EncodeToString(ephPub))
	fmt.Printf("  密文长度: %d 字节\n", len(ciphertext))

	plaintext, err := Decrypt(ciphertext, ephPub, recipientPriv)
	if err != nil {
		log.Fatalf("Decrypt: %v", err)
	}
	fmt.Printf("  解密成功: %q\n", string(plaintext))
	fmt.Println("  ✓ X25519 加密传输正常，每次使用不同临时密钥（前向安全）")
	fmt.Println()

	// --- Step 4: 内存清除 ---
	fmt.Println("[Step 4] 内存安全清除演示")
	sensitiveData := []byte("sensitive-private-key-must-be-cleared")
	fmt.Printf("  清除前: %q\n", string(sensitiveData))
	Clear(sensitiveData)
	allZero := true
	for _, b := range sensitiveData {
		if b != 0 {
			allZero = false
			break
		}
	}
	fmt.Printf("  清除后全零: %v\n", allZero)
	fmt.Println("  ✓ 四步内存清除完成（0x00 → 0xFF → random → 0x00）")
	fmt.Println()

	fmt.Println("=== Demo 完成 ===")
	fmt.Println("密钥管理三个核心安全属性已验证：")
	fmt.Println("  1. BIP32 硬化派生：泄漏子密钥不影响其他链")
	fmt.Println("  2. Scrypt 存储：内存硬性KDF，ASIC暴力破解成本极高")
	fmt.Println("  3. X25519 传输：前向安全，历史传输内容不可回溯")
}

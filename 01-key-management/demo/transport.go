package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// GenerateKeyPair 生成 X25519 密钥对（用于 ECDH 密钥交换）。
// X25519 用于密钥交换，Ed25519 用于签名，两者不可混用。
func GenerateKeyPair() (pub, priv [32]byte, err error) {
	if _, err = rand.Read(priv[:]); err != nil {
		return pub, priv, fmt.Errorf("transport: generate private key: %w", err)
	}
	// X25519 密钥清洗（RFC 7748 要求）
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	pubSlice, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return pub, priv, fmt.Errorf("transport: derive public key: %w", err)
	}
	copy(pub[:], pubSlice)
	return pub, priv, nil
}

// Encrypt 使用接收方公钥加密数据（每次生成临时密钥对，保证前向安全）。
func Encrypt(plaintext []byte, recipientPub [32]byte) (ciphertext, ephemeralPub []byte, err error) {
	var ePub, ePriv [32]byte
	ePub, ePriv, err = GenerateKeyPair()
	if err != nil {
		return nil, nil, fmt.Errorf("transport: generate ephemeral key: %w", err)
	}
	defer Clear(ePriv[:])

	sharedSecret, err := curve25519.X25519(ePriv[:], recipientPub[:])
	if err != nil {
		return nil, nil, fmt.Errorf("transport: ecdh: %w", err)
	}
	defer Clear(sharedSecret)

	// 派生对称加密密钥（生产中用 HKDF-SHA256，此处简化为 SHA256）
	encKey := sha256.Sum256(sharedSecret)
	defer Clear(encKey[:])

	block, err := aes.NewCipher(encKey[:])
	if err != nil {
		return nil, nil, fmt.Errorf("transport: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("transport: create gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("transport: generate nonce: %w", err)
	}

	ct := gcm.Seal(nonce, nonce, plaintext, nil)
	return ct, ePub[:], nil
}

// Decrypt 使用自己的私钥解密数据。
func Decrypt(ciphertext, ephemeralPubBytes []byte, myPriv [32]byte) ([]byte, error) {
	if len(ephemeralPubBytes) != 32 {
		return nil, fmt.Errorf("transport: ephemeral public key must be 32 bytes")
	}
	var ePub [32]byte
	copy(ePub[:], ephemeralPubBytes)

	sharedSecret, err := curve25519.X25519(myPriv[:], ePub[:])
	if err != nil {
		return nil, fmt.Errorf("transport: ecdh: %w", err)
	}
	defer Clear(sharedSecret)

	encKey := sha256.Sum256(sharedSecret)
	defer Clear(encKey[:])

	block, err := aes.NewCipher(encKey[:])
	if err != nil {
		return nil, fmt.Errorf("transport: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("transport: create gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("transport: ciphertext too short")
	}

	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ct, nil)
}

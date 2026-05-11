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

// Encrypt 使用接收方公钥加密数据（X25519 ECDH + AES-256-GCM）。
// 每次调用生成一个临时密钥对（ephemeral），用完即丢，保证前向安全：
// 即使接收方长期私钥泄露，历史密文也无法解密。
func Encrypt(plaintext []byte, recipientPub [32]byte) (ciphertext, ephemeralPub []byte, err error) {
	// 生成本次请求专用的临时密钥对，发送方用完即 defer Clear 销毁
	var ePub, ePriv [32]byte
	ePub, ePriv, err = GenerateKeyPair()
	if err != nil {
		return nil, nil, fmt.Errorf("transport: generate ephemeral key: %w", err)
	}
	defer Clear(ePriv[:]) // 临时私钥用完即销毁，历史请求无法重放

	// ECDH：ePriv × recipientPub = sharedSecret
	// 接收方用 myPriv × ePub 算出同一个 sharedSecret（乘法交换律）
	sharedSecret, err := curve25519.X25519(ePriv[:], recipientPub[:])
	if err != nil {
		return nil, nil, fmt.Errorf("transport: ecdh: %w", err)
	}
	defer Clear(sharedSecret)

	// 从共享密钥派生 AES 加密密钥（生产中用 HKDF-SHA256，此处简化为 SHA256）
	encKey := sha256.Sum256(sharedSecret)
	defer Clear(encKey[:])

	// AES-256-GCM（AEAD）：同时提供加密和消息认证，解密时 Tag 不对直接报错
	block, err := aes.NewCipher(encKey[:])
	if err != nil {
		return nil, nil, fmt.Errorf("transport: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("transport: create gcm: %w", err)
	}

	// 每次加密生成随机 nonce（12字节），防止相同密钥加密相同明文产生相同密文
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("transport: generate nonce: %w", err)
	}

	// gcm.Seal 把 nonce 前置拼在密文里，方便接收方拆分：ciphertext = [nonce || encrypted+tag]
	ct := gcm.Seal(nonce, nonce, plaintext, nil)
	return ct, ePub[:], nil // ePub 随密文一起发送，接收方用它做 ECDH
}

// Decrypt 使用自己的私钥和发送方临时公钥解密数据。
func Decrypt(ciphertext, ephemeralPubBytes []byte, myPriv [32]byte) ([]byte, error) {
	if len(ephemeralPubBytes) != 32 {
		return nil, fmt.Errorf("transport: ephemeral public key must be 32 bytes")
	}
	var ePub [32]byte
	copy(ePub[:], ephemeralPubBytes)

	// ECDH：myPriv × ePub = sharedSecret（和发送方算出的结果相同）
	sharedSecret, err := curve25519.X25519(myPriv[:], ePub[:])
	if err != nil {
		return nil, fmt.Errorf("transport: ecdh: %w", err)
	}
	defer Clear(sharedSecret)

	// 用相同方式派生 AES 密钥，必须和加密端完全一致
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

	// 拆分 nonce 和实际密文（加密时 Seal 把 nonce 前置了）
	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	// gcm.Open 同时解密并验证 Auth Tag，Tag 不对返回 error，防篡改
	return gcm.Open(nil, nonce, ct, nil)
}

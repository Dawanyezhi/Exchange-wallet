// Package main 密钥管理 demo：BIP32 派生 + Scrypt 存储 + X25519 传输 + 内存清除
package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"

	"golang.org/x/crypto/ed25519"
)

// ExtendedKey BIP32 扩展密钥（私钥 + 链码）。
type ExtendedKey struct {
	key    []byte // 64字节：前32字节是私钥，后32字节是链码
	pubkey []byte // 32字节：Ed25519 公钥
}

// NewMasterKey 从64字节种子（BIP39助记词派生结果）生成主密钥。
func NewMasterKey(seed []byte) (*ExtendedKey, error) {
	if len(seed) != 64 {
		return nil, fmt.Errorf("bip32: seed must be 64 bytes, got %d", len(seed))
	}

	mac := hmac.New(sha512.New, []byte("ed25519 seed"))
	mac.Write(seed)
	result := mac.Sum(nil)

	key := make([]byte, 64)
	copy(key, result)
	adjustEd25519Key(key[:32])

	pubkey := ed25519.NewKeyFromSeed(key[:32]).Public().(ed25519.PublicKey)

	return &ExtendedKey{
		key:    key,
		pubkey: []byte(pubkey),
	}, nil
}

// Child 按名称派生子密钥（硬化派生）。
func (k *ExtendedKey) Child(name string) (*ExtendedKey, error) {
	if k.key == nil {
		return nil, errors.New("bip32: key has been cleared")
	}

	index := nameToIndex(name)
	indexBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(indexBytes, index)

	// HMAC-SHA512(key=chainCode, data=0x00 || privkey || index)
	// 0x00 前缀表示硬化派生，防止从公钥推导子密钥
	mac := hmac.New(sha512.New, k.key[32:64])
	mac.Write([]byte{0x00})
	mac.Write(k.key[:32])
	mac.Write(indexBytes)
	result := mac.Sum(nil)

	childKey := make([]byte, 64)
	copy(childKey, result)
	adjustEd25519Key(childKey[:32])

	pubkey := ed25519.NewKeyFromSeed(childKey[:32]).Public().(ed25519.PublicKey)

	return &ExtendedKey{
		key:    childKey,
		pubkey: []byte(pubkey),
	}, nil
}

// PrivKeyBytes 返回私钥字节（32字节）。
func (k *ExtendedKey) PrivKeyBytes() []byte {
	if k.key == nil {
		return nil
	}
	out := make([]byte, 32)
	copy(out, k.key[:32])
	return out
}

// PubKeyBytes 返回公钥字节（32字节）。
func (k *ExtendedKey) PubKeyBytes() []byte {
	if k.pubkey == nil {
		return nil
	}
	out := make([]byte, 32)
	copy(out, k.pubkey)
	return out
}

// ClearKey 四步内存清除。
func (k *ExtendedKey) ClearKey() {
	if k.key != nil {
		clearBytes(k.key)
		k.key = nil
	}
	if k.pubkey != nil {
		clearBytes(k.pubkey)
		k.pubkey = nil
	}
}

func adjustEd25519Key(key []byte) {
	key[0] &= 248  // 最低3位清零（防止小子群攻击）
	key[31] &= 127 // 最高位清零（防止溢出曲线阶）
	key[31] |= 64  // 第二高位置1（确保密钥强度）
}

func nameToIndex(name string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(name); i++ {
		h ^= uint32(name[i])
		h *= 16777619
	}
	return h | (1 << 31) // 硬化派生：index >= 2^31
}

func clearBytes(b []byte) {
	for i := range b {
		b[i] = 0x00
	}
	for i := range b {
		b[i] = 0xFF
	}
	// 真随机覆盖：防止编译器识别出确定性模式并优化掉前两步
	rand.Read(b) //nolint:errcheck // rand.Read on Linux/macOS never fails
	for i := range b {
		b[i] = 0x00
	}
	runtime.KeepAlive(b)
}

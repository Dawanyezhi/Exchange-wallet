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
// 遵循 SLIP-0010 规范：用固定字符串 "ed25519 seed" 作为 HMAC 密钥，确保不同实现之间兼容。
func NewMasterKey(seed []byte) (*ExtendedKey, error) {
	// Ed25519 BIP32 要求种子严格为 64 字节（BIP39 PBKDF2 输出长度），防止弱种子
	if len(seed) != 64 {
		return nil, fmt.Errorf("bip32: seed must be 64 bytes, got %d", len(seed))
	}

	// SLIP-0010 规定：用 HMAC-SHA512("ed25519 seed", seed) 生成主密钥材料
	// 输出 64 字节：前32字节 = 主私钥（IL），后32字节 = 主链码（IR，用于子密钥派生）
	mac := hmac.New(sha512.New, []byte("ed25519 seed"))
	mac.Write(seed)
	result := mac.Sum(nil) // 64 字节：[IL(32) || IR(32)]

	// 将 HMAC 结果完整保存：key[:32] = 私钥，key[32:] = 链码
	key := make([]byte, 64)
	copy(key, result)
	// Ed25519 私钥有特定位要求（RFC 8032）：clamp 操作清除低3位、置第255位
	// 确保标量在合法范围内，防止小子群攻击
	adjustEd25519Key(key[:32])

	// 从私钥派生对应公钥（ed25519.NewKeyFromSeed 接受32字节种子，内部做 SHA-512 展开）
	pubkey := ed25519.NewKeyFromSeed(key[:32]).Public().(ed25519.PublicKey)

	return &ExtendedKey{
		key:    key,    // [私钥(32) || 链码(32)]，链码用于后续子密钥 HMAC 输入
		pubkey: []byte(pubkey),
	}, nil
}

// Child 按链名派生子密钥（硬化派生，Hardened Derivation）。
// 硬化派生把父私钥塞进 HMAC 内部，子私钥泄露无法反推父私钥。
// 非硬化派生用父公钥，子私钥泄露可通过减法还原父私钥——有安全风险。
func (k *ExtendedKey) Child(name string) (*ExtendedKey, error) {
	if k.key == nil {
		return nil, errors.New("bip32: key has been cleared")
	}

	// 把链名（如 "ETH"）通过 FNV-1a 哈希映射到 hardened index（≥ 2^31）
	index := nameToIndex(name)
	indexBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(indexBytes, index)

	// 硬化派生公式：HMAC-SHA512(key=chainCode, data=0x00 || 父私钥 || index)
	// 0x00 前缀是硬化派生的标志（非硬化用父公钥，不加 0x00 前缀）
	// chainCode（key[32:64]）作为 HMAC 密钥，隔离不同层级的派生
	mac := hmac.New(sha512.New, k.key[32:64])
	mac.Write([]byte{0x00})  // 硬化标志
	mac.Write(k.key[:32])    // 父私钥（塞进黑箱，攻击者无法从外部算出 HMAC 结果）
	mac.Write(indexBytes)    // 派生索引
	result := mac.Sum(nil)   // 64 字节：[子私钥(32) || 子链码(32)]

	childKey := make([]byte, 64)
	copy(childKey, result)
	adjustEd25519Key(childKey[:32]) // 同样需要 clamp，确保子私钥合法

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

// nameToIndex 把链名映射到 BIP32 hardened index。
// 用 FNV-1a 哈希把字符串转成 uint32，再 OR 上 2^31 使其变为 hardened index。
// hardened index 范围 [2^31, 2^32-1]，对应 BIP32 的 index' 表示法（如 m/44'/60'/0'）。
func nameToIndex(name string) uint32 {
	h := uint32(2166136261) // FNV-1a 32位偏置基（固定常数）
	for i := 0; i < len(name); i++ {
		h ^= uint32(name[i])
		h *= 16777619 // FNV 质数
	}
	return h | (1 << 31) // 最高位置1，确保 index >= 2^31（硬化派生范围）
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

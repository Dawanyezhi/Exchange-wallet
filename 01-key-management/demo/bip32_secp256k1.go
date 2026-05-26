package main

import (
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/pbkdf2"
)

const (
	HardenedKeyStart uint32 = 0x80000000
)

var (
	secp256k1N = crypto.S256().Params().N

	errInvalidSecp256k1Key = errors.New("bip32 secp256k1: invalid private key")
)

// Secp256k1ExtendedKey 是 EVM 可用的 BIP32 扩展私钥。
//
// key 保存 32 字节 secp256k1 私钥，chainCode 保存 32 字节链码。
// EVM 生产派生通常走 BIP44 路径：m/44'/60'/account'/change/index。
type Secp256k1ExtendedKey struct {
	key       []byte
	chainCode []byte
}

// MnemonicToSeed 按 BIP39 把助记词转换成 64 字节 seed。
// passphrase 为空时等价于常见钱包的默认导入方式。
func MnemonicToSeed(mnemonic, passphrase string) []byte {
	salt := "mnemonic" + passphrase
	return pbkdf2.Key([]byte(mnemonic), []byte(salt), 2048, 64, sha512.New)
}

// NewSecp256k1MasterKey 从 BIP39 seed 生成 BIP32 secp256k1 master key。
//
// BIP32 标准固定使用 HMAC-SHA512(key="Bitcoin seed", data=seed)。
// 这里的 "Bitcoin seed" 是 BIP32 域分离常量，不表示只能用于 BTC。
func NewSecp256k1MasterKey(seed []byte) (*Secp256k1ExtendedKey, error) {
	if len(seed) < 16 {
		return nil, fmt.Errorf("bip32 secp256k1: seed must be at least 16 bytes, got %d", len(seed))
	}

	mac := hmac.New(sha512.New, []byte("Bitcoin seed"))
	if _, err := mac.Write(seed); err != nil {
		return nil, fmt.Errorf("bip32 secp256k1: hmac seed: %w", err)
	}
	result := mac.Sum(nil)

	key := make([]byte, 32)
	chainCode := make([]byte, 32)
	copy(key, result[:32])
	copy(chainCode, result[32:])

	if !isValidSecp256k1PrivateKey(key) {
		clearBytes(key)
		clearBytes(chainCode)
		return nil, errInvalidSecp256k1Key
	}

	return &Secp256k1ExtendedKey{
		key:       key,
		chainCode: chainCode,
	}, nil
}

// Child 派生子扩展私钥。index >= HardenedKeyStart 时为 hardened 派生。
func (k *Secp256k1ExtendedKey) Child(index uint32) (*Secp256k1ExtendedKey, error) {
	if k == nil || k.key == nil || k.chainCode == nil {
		return nil, errors.New("bip32 secp256k1: key has been cleared")
	}

	data := make([]byte, 0, 37)
	if index >= HardenedKeyStart {
		data = append(data, 0x00)
		data = append(data, k.key...)
	} else {
		priv, err := k.PrivateKey()
		if err != nil {
			return nil, err
		}
		data = append(data, crypto.CompressPubkey(&priv.PublicKey)...)
	}

	indexBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(indexBytes, index)
	data = append(data, indexBytes...)

	mac := hmac.New(sha512.New, k.chainCode)
	if _, err := mac.Write(data); err != nil {
		return nil, fmt.Errorf("bip32 secp256k1: hmac child: %w", err)
	}
	result := mac.Sum(nil)
	il, childChainCode := result[:32], result[32:]

	ilInt := new(big.Int).SetBytes(il)
	if ilInt.Sign() == 0 || ilInt.Cmp(secp256k1N) >= 0 {
		return nil, errInvalidSecp256k1Key
	}

	parentInt := new(big.Int).SetBytes(k.key)
	childInt := ilInt.Add(ilInt, parentInt)
	childInt.Mod(childInt, secp256k1N)
	if childInt.Sign() == 0 {
		return nil, errInvalidSecp256k1Key
	}

	childKey := intTo32Bytes(childInt)
	childCode := make([]byte, 32)
	copy(childCode, childChainCode)

	return &Secp256k1ExtendedKey{
		key:       childKey,
		chainCode: childCode,
	}, nil
}

// DerivePath 派生 BIP32/BIP44 路径，例如 m/44'/60'/0'/0/0。
func (k *Secp256k1ExtendedKey) DerivePath(path string) (*Secp256k1ExtendedKey, error) {
	indexes, err := ParseBIP32Path(path)
	if err != nil {
		return nil, err
	}

	current := k
	for _, index := range indexes {
		child, err := current.Child(index)
		if err != nil {
			return nil, fmt.Errorf("bip32 secp256k1: derive %s: %w", formatPathIndex(index), err)
		}
		current = child
	}
	return current, nil
}

// PrivateKey 返回 go-ethereum 可直接用于签名的 secp256k1 私钥。
func (k *Secp256k1ExtendedKey) PrivateKey() (*ecdsa.PrivateKey, error) {
	if k == nil || k.key == nil {
		return nil, errors.New("bip32 secp256k1: key has been cleared")
	}
	return crypto.ToECDSA(k.key)
}

// PrivateKeyBytes 返回 32 字节私钥副本。
func (k *Secp256k1ExtendedKey) PrivateKeyBytes() []byte {
	if k == nil || k.key == nil {
		return nil
	}
	out := make([]byte, 32)
	copy(out, k.key)
	return out
}

// PrivateKeyHex 返回 64 字符十六进制私钥，不带 0x 前缀。
func (k *Secp256k1ExtendedKey) PrivateKeyHex() string {
	return hex.EncodeToString(k.PrivateKeyBytes())
}

// PublicKeyBytes 返回公钥字节。compressed=true 返回 33 字节压缩公钥，否则返回 65 字节未压缩公钥。
func (k *Secp256k1ExtendedKey) PublicKeyBytes(compressed bool) ([]byte, error) {
	priv, err := k.PrivateKey()
	if err != nil {
		return nil, err
	}
	if compressed {
		return crypto.CompressPubkey(&priv.PublicKey), nil
	}
	return crypto.FromECDSAPub(&priv.PublicKey), nil
}

// EthereumAddress 返回该私钥对应的 EVM 地址。
func (k *Secp256k1ExtendedKey) EthereumAddress() (common.Address, error) {
	priv, err := k.PrivateKey()
	if err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(priv.PublicKey), nil
}

// ClearKey 清除扩展私钥和链码。
func (k *Secp256k1ExtendedKey) ClearKey() {
	if k == nil {
		return
	}
	if k.key != nil {
		clearBytes(k.key)
		k.key = nil
	}
	if k.chainCode != nil {
		clearBytes(k.chainCode)
		k.chainCode = nil
	}
}

// ParseBIP32Path 解析 m/44'/60'/0'/0/0 形式的路径。
func ParseBIP32Path(path string) ([]uint32, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "m" || path == "M" {
		return nil, nil
	}
	if !strings.HasPrefix(path, "m/") && !strings.HasPrefix(path, "M/") {
		return nil, fmt.Errorf("bip32 secp256k1: path must start with m/, got %q", path)
	}

	parts := strings.Split(path[2:], "/")
	indexes := make([]uint32, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("bip32 secp256k1: empty path segment in %q", path)
		}

		hardened := false
		last := part[len(part)-1]
		if last == '\'' || last == 'h' || last == 'H' {
			hardened = true
			part = part[:len(part)-1]
		}
		if part == "" {
			return nil, fmt.Errorf("bip32 secp256k1: empty path segment in %q", path)
		}

		value, err := strconv.ParseUint(part, 10, 31)
		if err != nil {
			return nil, fmt.Errorf("bip32 secp256k1: invalid path segment %q: %w", part, err)
		}

		index := uint32(value)
		if hardened {
			index += HardenedKeyStart
		}
		indexes = append(indexes, index)
	}
	return indexes, nil
}

func isValidSecp256k1PrivateKey(key []byte) bool {
	keyInt := new(big.Int).SetBytes(key)
	return keyInt.Sign() > 0 && keyInt.Cmp(secp256k1N) < 0
}

func intTo32Bytes(v *big.Int) []byte {
	out := make([]byte, 32)
	b := v.Bytes()
	copy(out[32-len(b):], b)
	return out
}

func formatPathIndex(index uint32) string {
	if index >= HardenedKeyStart {
		return fmt.Sprintf("%d'", index-HardenedKeyStart)
	}
	return strconv.FormatUint(uint64(index), 10)
}

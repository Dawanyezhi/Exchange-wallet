package main

import (
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/pbkdf2"
)

const hardenedKeyStart uint32 = 0x80000000

var secp256k1Order = crypto.S256().Params().N

type hdPrivateKey struct {
	key       []byte
	chainCode []byte
}

func mnemonicSeed(mnemonic, passphrase string) []byte {
	return pbkdf2.Key([]byte(mnemonic), []byte("mnemonic"+passphrase), 2048, 64, sha512.New)
}

func newMasterKey(seed []byte) (*hdPrivateKey, error) {
	if len(seed) < 16 {
		return nil, fmt.Errorf("bip32 seed too short: %d", len(seed))
	}
	mac := hmac.New(sha512.New, []byte("Bitcoin seed"))
	_, _ = mac.Write(seed)
	sum := mac.Sum(nil)
	key := append([]byte(nil), sum[:32]...)
	if !validPrivateKey(key) {
		return nil, errors.New("bip32 master key is invalid")
	}
	return &hdPrivateKey{
		key:       key,
		chainCode: append([]byte(nil), sum[32:]...),
	}, nil
}

func (k *hdPrivateKey) child(index uint32) (*hdPrivateKey, error) {
	if k == nil || len(k.key) != 32 || len(k.chainCode) != 32 {
		return nil, errors.New("bip32 private key has been cleared")
	}
	data := make([]byte, 0, 37)
	if index >= hardenedKeyStart {
		data = append(data, 0x00)
		data = append(data, k.key...)
	} else {
		pub, err := k.compressedPubKey()
		if err != nil {
			return nil, err
		}
		data = append(data, pub...)
	}
	var idx [4]byte
	binary.BigEndian.PutUint32(idx[:], index)
	data = append(data, idx[:]...)

	mac := hmac.New(sha512.New, k.chainCode)
	_, _ = mac.Write(data)
	sum := mac.Sum(nil)

	il := new(big.Int).SetBytes(sum[:32])
	if il.Sign() == 0 || il.Cmp(secp256k1Order) >= 0 {
		return nil, errors.New("bip32 child IL is invalid")
	}
	parent := new(big.Int).SetBytes(k.key)
	child := il.Add(il, parent)
	child.Mod(child, secp256k1Order)
	if child.Sign() == 0 {
		return nil, errors.New("bip32 child key is invalid")
	}
	return &hdPrivateKey{
		key:       intTo32(child),
		chainCode: append([]byte(nil), sum[32:]...),
	}, nil
}

func (k *hdPrivateKey) derive(path string) (*hdPrivateKey, error) {
	indexes, err := parsePath(path)
	if err != nil {
		return nil, err
	}
	current := k
	for _, index := range indexes {
		next, err := current.child(index)
		if err != nil {
			return nil, fmt.Errorf("derive %s: %w", path, err)
		}
		current = next
	}
	return current, nil
}

func (k *hdPrivateKey) privateKey() (*ecdsa.PrivateKey, error) {
	if k == nil || len(k.key) != 32 {
		return nil, errors.New("bip32 private key has been cleared")
	}
	return crypto.ToECDSA(k.key)
}

func (k *hdPrivateKey) compressedPubKey() ([]byte, error) {
	priv, err := k.privateKey()
	if err != nil {
		return nil, err
	}
	return crypto.CompressPubkey(&priv.PublicKey), nil
}

func (k *hdPrivateKey) clear() {
	if k == nil {
		return
	}
	for i := range k.key {
		k.key[i] = 0
	}
	for i := range k.chainCode {
		k.chainCode[i] = 0
	}
	k.key = nil
	k.chainCode = nil
}

func parsePath(path string) ([]uint32, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "m" || path == "M" {
		return nil, nil
	}
	if !strings.HasPrefix(path, "m/") && !strings.HasPrefix(path, "M/") {
		return nil, fmt.Errorf("path must start with m/: %q", path)
	}
	parts := strings.Split(path[2:], "/")
	out := make([]uint32, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("empty path segment in %q", path)
		}
		hardened := false
		last := part[len(part)-1]
		if last == '\'' || last == 'h' || last == 'H' {
			hardened = true
			part = part[:len(part)-1]
		}
		value, err := strconv.ParseUint(part, 10, 31)
		if err != nil {
			return nil, fmt.Errorf("invalid path segment %q: %w", part, err)
		}
		index := uint32(value)
		if hardened {
			index += hardenedKeyStart
		}
		out = append(out, index)
	}
	return out, nil
}

func validPrivateKey(key []byte) bool {
	n := new(big.Int).SetBytes(key)
	return n.Sign() > 0 && n.Cmp(secp256k1Order) < 0
}

func intTo32(n *big.Int) []byte {
	out := make([]byte, 32)
	b := n.Bytes()
	copy(out[32-len(b):], b)
	return out
}

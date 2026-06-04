package main

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const hardenedKeyStart uint32 = 0x80000000

type slip10PrivateKey struct {
	key       []byte
	chainCode []byte
}

func mnemonicSeed(mnemonic, passphrase string) []byte {
	return pbkdf2.Key([]byte(mnemonic), []byte("mnemonic"+passphrase), 2048, 64, sha512.New)
}

func newSLIP10Master(seed []byte) (*slip10PrivateKey, error) {
	if len(seed) < 16 {
		return nil, fmt.Errorf("slip10 seed too short: %d", len(seed))
	}
	mac := hmac.New(sha512.New, []byte("ed25519 seed"))
	_, _ = mac.Write(seed)
	sum := mac.Sum(nil)
	return &slip10PrivateKey{
		key:       append([]byte(nil), sum[:32]...),
		chainCode: append([]byte(nil), sum[32:]...),
	}, nil
}

func (k *slip10PrivateKey) child(index uint32) (*slip10PrivateKey, error) {
	if index < hardenedKeyStart {
		return nil, errors.New("slip10 ed25519 only supports hardened child derivation")
	}
	if k == nil || len(k.key) != 32 || len(k.chainCode) != 32 {
		return nil, errors.New("slip10 key has been cleared")
	}
	data := make([]byte, 0, 37)
	data = append(data, 0x00)
	data = append(data, k.key...)
	var idx [4]byte
	binary.BigEndian.PutUint32(idx[:], index)
	data = append(data, idx[:]...)

	mac := hmac.New(sha512.New, k.chainCode)
	_, _ = mac.Write(data)
	sum := mac.Sum(nil)
	return &slip10PrivateKey{
		key:       append([]byte(nil), sum[:32]...),
		chainCode: append([]byte(nil), sum[32:]...),
	}, nil
}

func (k *slip10PrivateKey) derive(path string) (*slip10PrivateKey, error) {
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

func (k *slip10PrivateKey) privateKey() ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(k.key)
}

func (k *slip10PrivateKey) publicKey() []byte {
	priv := k.privateKey()
	return append([]byte(nil), priv.Public().(ed25519.PublicKey)...)
}

func (k *slip10PrivateKey) address() string {
	return encodeBase58(k.publicKey())
}

func (k *slip10PrivateKey) clear() {
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
		last := part[len(part)-1]
		if last != '\'' && last != 'h' && last != 'H' {
			return nil, fmt.Errorf("solana slip10 path segment must be hardened: %q", part)
		}
		value, err := strconv.ParseUint(part[:len(part)-1], 10, 31)
		if err != nil {
			return nil, fmt.Errorf("invalid path segment %q: %w", part, err)
		}
		out = append(out, uint32(value)+hardenedKeyStart)
	}
	return out, nil
}

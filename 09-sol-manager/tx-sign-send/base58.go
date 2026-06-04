package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
)

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var base58Index = func() map[rune]int {
	out := make(map[rune]int, len(base58Alphabet))
	for i, r := range base58Alphabet {
		out[r] = i
	}
	return out
}()

func encodeBase58(input []byte) string {
	x := new(big.Int).SetBytes(input)
	base := big.NewInt(58)
	zero := big.NewInt(0)
	mod := new(big.Int)
	var out []byte
	for x.Cmp(zero) > 0 {
		x.DivMod(x, base, mod)
		out = append(out, base58Alphabet[mod.Int64()])
	}
	for _, b := range input {
		if b != 0 {
			break
		}
		out = append(out, base58Alphabet[0])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

func decodeBase58(input string) ([]byte, error) {
	if input == "" {
		return nil, errors.New("base58: empty string")
	}
	result := big.NewInt(0)
	base := big.NewInt(58)
	for _, r := range input {
		value, ok := base58Index[r]
		if !ok {
			return nil, fmt.Errorf("base58: invalid char %q", r)
		}
		result.Mul(result, base)
		result.Add(result, big.NewInt(int64(value)))
	}
	decoded := result.Bytes()
	leadingZeros := 0
	for _, r := range input {
		if r != '1' {
			break
		}
		leadingZeros++
	}
	if leadingZeros > 0 {
		decoded = append(bytes.Repeat([]byte{0}, leadingZeros), decoded...)
	}
	return decoded, nil
}

func mustDecodePubkey(address string) ([32]byte, error) {
	var out [32]byte
	raw, err := decodeBase58(address)
	if err != nil {
		return out, err
	}
	if len(raw) != 32 {
		return out, fmt.Errorf("solana pubkey must be 32 bytes, got %d", len(raw))
	}
	copy(out[:], raw)
	return out, nil
}

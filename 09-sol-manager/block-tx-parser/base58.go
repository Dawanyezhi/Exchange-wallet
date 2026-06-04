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

func isValidPubkey(address string) bool {
	raw, err := decodeBase58(address)
	return err == nil && len(raw) == 32
}

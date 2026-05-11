// Package main Shamir 秘密分享演示（MPC 基础构件）。
// Shamir(k, n)：n 个份额，任意 k 个可恢复原始秘密。
// 原理：构造 k-1 次多项式 f(x)，秘密 = f(0)，份额 = f(1), f(2), ..., f(n)。
package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
)

// 使用大素数作为有限域
// 选择比 256 位密钥大的素数
var prime, _ = new(big.Int).SetString(
	"FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFFC2F",
	16,
)

// Share 秘密份额。
type Share struct {
	X *big.Int // 份额编号（1, 2, ..., n）
	Y *big.Int // 份额值（f(X)）
}

// Split 将 secret 分割为 n 个份额，任意 k 个可恢复。
// k: 恢复所需的最小份额数
// n: 总份额数（n >= k）
func Split(secret []byte, k, n int) ([]Share, error) {
	if k > n {
		return nil, errors.New("shamir: threshold k cannot exceed total shares n")
	}
	if k < 2 {
		return nil, errors.New("shamir: threshold k must be at least 2")
	}
	if len(secret) == 0 {
		return nil, errors.New("shamir: secret cannot be empty")
	}

	s := new(big.Int).SetBytes(secret)
	if s.Cmp(prime) >= 0 {
		return nil, fmt.Errorf("shamir: secret must be less than prime field modulus")
	}

	// 构造 k-1 次多项式：f(x) = secret + a1*x + a2*x^2 + ... + a_{k-1}*x^{k-1}
	// 系数 a1..a_{k-1} 随机生成
	coefficients := make([]*big.Int, k)
	coefficients[0] = new(big.Int).Set(s) // f(0) = secret

	for i := 1; i < k; i++ {
		coeff, err := rand.Int(rand.Reader, prime)
		if err != nil {
			return nil, fmt.Errorf("shamir: generate coefficient: %w", err)
		}
		coefficients[i] = coeff
	}

	// 计算 n 个份额：f(1), f(2), ..., f(n)
	shares := make([]Share, n)
	for i := 1; i <= n; i++ {
		x := big.NewInt(int64(i))
		y := evalPolynomial(coefficients, x)
		shares[i-1] = Share{X: x, Y: y}
	}

	return shares, nil
}

// Reconstruct 从至少 k 个份额恢复原始秘密（使用拉格朗日插值）。
func Reconstruct(shares []Share) ([]byte, error) {
	if len(shares) < 2 {
		return nil, errors.New("shamir: need at least 2 shares")
	}

	// 拉格朗日插值：计算 f(0) = Σ y_i * Π_{j≠i} (0 - x_j) / (x_i - x_j)
	secret := big.NewInt(0)

	for i, si := range shares {
		numerator := big.NewInt(1)
		denominator := big.NewInt(1)

		for j, sj := range shares {
			if i == j {
				continue
			}
			// numerator *= (0 - x_j) = -x_j
			neg := new(big.Int).Neg(sj.X)
			neg.Mod(neg, prime)
			numerator.Mul(numerator, neg)
			numerator.Mod(numerator, prime)

			// denominator *= (x_i - x_j)
			diff := new(big.Int).Sub(si.X, sj.X)
			diff.Mod(diff, prime)
			denominator.Mul(denominator, diff)
			denominator.Mod(denominator, prime)
		}

		// term = y_i * numerator / denominator (mod prime)
		// 除法用乘法逆元代替
		denomInv := new(big.Int).ModInverse(denominator, prime)
		if denomInv == nil {
			return nil, errors.New("shamir: modular inverse does not exist")
		}

		term := new(big.Int).Mul(si.Y, numerator)
		term.Mod(term, prime)
		term.Mul(term, denomInv)
		term.Mod(term, prime)

		secret.Add(secret, term)
		secret.Mod(secret, prime)
	}

	return secret.Bytes(), nil
}

// evalPolynomial 计算多项式 f(x) = Σ coefficients[i] * x^i (mod prime)。
func evalPolynomial(coefficients []*big.Int, x *big.Int) *big.Int {
	result := big.NewInt(0)
	xPow := big.NewInt(1) // x^0 = 1

	for _, coeff := range coefficients {
		term := new(big.Int).Mul(coeff, xPow)
		term.Mod(term, prime)
		result.Add(result, term)
		result.Mod(result, prime)

		// xPow *= x
		xPow.Mul(xPow, x)
		xPow.Mod(xPow, prime)
	}
	return result
}

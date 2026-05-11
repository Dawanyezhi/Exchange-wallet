// Package bigint 封装 math/big.Int，解决 JSON 序列化和数据库存储中的精度问题。
// 生产中所有金额都用 bigint.Int，禁止用 float64（精度损失）。
package bigint

import (
	"database/sql/driver"
	"fmt"
	"math/big"
	"strings"
)

// Int 封装 big.Int，提供数据库和 JSON 友好的金额类型。
type Int struct {
	v *big.Int
}

// Zero 返回值为0的 Int。
func Zero() Int {
	return Int{v: new(big.Int)}
}

// FromString 从十进制字符串解析（链上格式，最小单位，如 wei）。
func FromString(s string) (Int, error) {
	if s == "" || s == "0" {
		return Zero(), nil
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return Int{}, fmt.Errorf("bigint: invalid decimal string %q", s)
	}
	return Int{v: v}, nil
}

// MustFromString 解析失败时 panic，用于测试和常量初始化。
func MustFromString(s string) Int {
	i, err := FromString(s)
	if err != nil {
		panic(err)
	}
	return i
}

// FromReadable 将人类可读格式（如 "1.5"）转为链上格式（如 "1500000000000000000"）。
// decimals 是代币精度，如 ETH=18，USDT=6。
func FromReadable(s string, decimals int) (Int, error) {
	s = strings.TrimSpace(s)
	parts := strings.SplitN(s, ".", 2)

	intPart := parts[0]
	fracPart := ""
	if len(parts) == 2 {
		fracPart = parts[1]
	}

	// 截断或补零到 decimals 位
	if len(fracPart) > decimals {
		fracPart = fracPart[:decimals]
	} else {
		fracPart = fracPart + strings.Repeat("0", decimals-len(fracPart))
	}

	combined := intPart + fracPart
	// 去掉前导零
	combined = strings.TrimLeft(combined, "0")
	if combined == "" {
		combined = "0"
	}

	return FromString(combined)
}

// String 返回十进制字符串（数据库存储格式）。
func (i Int) String() string {
	if i.v == nil {
		return "0"
	}
	return i.v.String()
}

// Readable 将链上格式转为人类可读格式。
// 例：Readable(18) 将 "1000000000000000000" 转为 "1.000000000000000000"。
func (i Int) Readable(decimals int) string {
	if i.v == nil || i.v.Sign() == 0 {
		return "0." + strings.Repeat("0", decimals)
	}

	s := i.v.String()
	if len(s) <= decimals {
		s = strings.Repeat("0", decimals-len(s)+1) + s
	}

	pos := len(s) - decimals
	return s[:pos] + "." + s[pos:]
}

// BigInt 返回底层 *big.Int（只读，勿修改）。
func (i Int) BigInt() *big.Int {
	if i.v == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(i.v)
}

// Add 返回 i + other。
func (i Int) Add(other Int) Int {
	result := new(big.Int)
	if i.v != nil {
		result.Set(i.v)
	}
	if other.v != nil {
		result.Add(result, other.v)
	}
	return Int{v: result}
}

// Sub 返回 i - other。
func (i Int) Sub(other Int) Int {
	result := new(big.Int)
	if i.v != nil {
		result.Set(i.v)
	}
	if other.v != nil {
		result.Sub(result, other.v)
	}
	return Int{v: result}
}

// Cmp 比较 i 和 other：i<other 返回-1，i==other 返回0，i>other 返回1。
func (i Int) Cmp(other Int) int {
	a := i.v
	if a == nil {
		a = new(big.Int)
	}
	b := other.v
	if b == nil {
		b = new(big.Int)
	}
	return a.Cmp(b)
}

// IsZero 返回是否为零。
func (i Int) IsZero() bool {
	return i.v == nil || i.v.Sign() == 0
}

// IsPositive 返回是否为正数。
func (i Int) IsPositive() bool {
	return i.v != nil && i.v.Sign() > 0
}

// Scan 实现 database/sql.Scanner，支持从数据库读取。
func (i *Int) Scan(src interface{}) error {
	switch v := src.(type) {
	case string:
		parsed, err := FromString(v)
		if err != nil {
			return fmt.Errorf("bigint.Scan: %w", err)
		}
		*i = parsed
	case []byte:
		parsed, err := FromString(string(v))
		if err != nil {
			return fmt.Errorf("bigint.Scan: %w", err)
		}
		*i = parsed
	case nil:
		*i = Zero()
	default:
		return fmt.Errorf("bigint.Scan: unsupported type %T", src)
	}
	return nil
}

// Value 实现 database/sql/driver.Valuer，支持写入数据库。
func (i Int) Value() (driver.Value, error) {
	return i.String(), nil
}

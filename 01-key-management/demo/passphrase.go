package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"golang.org/x/crypto/scrypt"
)

const (
	// StandardScryptN 标准 Scrypt N 参数（生产用）：需要 256MB 内存，约 1 秒。
	StandardScryptN = 1 << 18 // 262144
	// LightScryptN 轻量 N 参数（测试用）：快速验证功能，不用于生产。
	LightScryptN = 1 << 12 // 4096

	StandardScryptP = 1
	scryptR         = 8
	scryptDKLen     = 32
)

// CryptoJSON 兼容以太坊 Web3 Secret Storage 规范的加密存储格式。
type CryptoJSON struct {
	Cipher       string                 `json:"cipher"`
	CipherText   string                 `json:"ciphertext"`
	CipherParams CipherParams           `json:"cipherparams"`
	KDF          string                 `json:"kdf"`
	KDFParams    map[string]interface{} `json:"kdfparams"`
	MAC          string                 `json:"mac"`
}

// CipherParams AES-CTR 参数。
type CipherParams struct {
	IV string `json:"iv"`
}

// EncryptData 加密数据。scryptN 生产用 StandardScryptN，测试用 LightScryptN。
func EncryptData(data, passphrase []byte, scryptN, scryptP int) (CryptoJSON, error) {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return CryptoJSON{}, fmt.Errorf("passphrase: generate salt: %w", err)
	}

	derivedKey, err := scrypt.Key(passphrase, salt, scryptN, scryptR, scryptP, scryptDKLen)
	if err != nil {
		return CryptoJSON{}, fmt.Errorf("passphrase: scrypt: %w", err)
	}
	defer clearBytes(derivedKey)

	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return CryptoJSON{}, fmt.Errorf("passphrase: generate iv: %w", err)
	}

	ciphertext, err := aesCTRXOR(derivedKey[:16], data, iv)
	if err != nil {
		return CryptoJSON{}, fmt.Errorf("passphrase: encrypt: %w", err)
	}

	mac := calculateMAC(derivedKey[16:32], ciphertext)

	return CryptoJSON{
		Cipher:     "aes-128-ctr",
		CipherText: hex.EncodeToString(ciphertext),
		CipherParams: CipherParams{
			IV: hex.EncodeToString(iv),
		},
		KDF: "scrypt",
		KDFParams: map[string]interface{}{
			"n":     scryptN,
			"r":     scryptR,
			"p":     scryptP,
			"dklen": scryptDKLen,
			"salt":  hex.EncodeToString(salt),
		},
		MAC: hex.EncodeToString(mac),
	}, nil
}

// DecryptData 解密数据。先验 MAC，验证失败不解密。
func DecryptData(cj CryptoJSON, passphrase string) ([]byte, error) {
	if cj.Cipher != "aes-128-ctr" {
		return nil, fmt.Errorf("passphrase: unsupported cipher %q", cj.Cipher)
	}

	mac, err := hex.DecodeString(cj.MAC)
	if err != nil {
		return nil, fmt.Errorf("passphrase: decode mac: %w", err)
	}
	ciphertext, err := hex.DecodeString(cj.CipherText)
	if err != nil {
		return nil, fmt.Errorf("passphrase: decode ciphertext: %w", err)
	}
	iv, err := hex.DecodeString(cj.CipherParams.IV)
	if err != nil {
		return nil, fmt.Errorf("passphrase: decode iv: %w", err)
	}

	salt, n, r, p, dklen, err := parseKDFParams(cj.KDFParams)
	if err != nil {
		return nil, err
	}

	derivedKey, err := scrypt.Key([]byte(passphrase), salt, n, r, p, dklen)
	if err != nil {
		return nil, fmt.Errorf("passphrase: scrypt: %w", err)
	}
	defer clearBytes(derivedKey)

	// 先验 MAC（防止 padding oracle 攻击）
	expectedMAC := calculateMAC(derivedKey[16:32], ciphertext)
	if !constTimeEqual(expectedMAC, mac) {
		return nil, fmt.Errorf("passphrase: mac mismatch, wrong passphrase or corrupted data")
	}

	return aesCTRXOR(derivedKey[:16], ciphertext, iv)
}

func aesCTRXOR(key, inText, iv []byte) ([]byte, error) {
	aesBlock, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	stream := cipher.NewCTR(aesBlock, iv)
	outText := make([]byte, len(inText))
	stream.XORKeyStream(outText, inText)
	return outText, nil
}

func calculateMAC(derivedKey, ciphertext []byte) []byte {
	h := sha256.New()
	h.Write(derivedKey)
	h.Write(ciphertext)
	return h.Sum(nil)
}

func constTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func parseKDFParams(params map[string]interface{}) (salt []byte, n, r, p, dklen int, err error) {
	saltHex, ok := params["salt"].(string)
	if !ok {
		return nil, 0, 0, 0, 0, fmt.Errorf("passphrase: missing salt")
	}
	salt, err = hex.DecodeString(saltHex)
	if err != nil {
		return nil, 0, 0, 0, 0, fmt.Errorf("passphrase: decode salt: %w", err)
	}
	n = int(toFloat64(params["n"]))
	r = int(toFloat64(params["r"]))
	p = int(toFloat64(params["p"]))
	dklen = int(toFloat64(params["dklen"]))
	if n == 0 || r == 0 || p == 0 || dklen == 0 {
		return nil, 0, 0, 0, 0, fmt.Errorf("passphrase: invalid kdf params")
	}
	return
}

func toFloat64(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	}
	return 0
}

package main

import (
	"bytes"
	"testing"
)

// TestShamir2of3 验证 3 份额中任意 2 个可恢复，任意 1 个不可恢复。
func TestShamir2of3(t *testing.T) {
	secret := []byte("my-super-secret-private-key-here") // 32字节
	shares, err := Split(secret, 2, 3)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if len(shares) != 3 {
		t.Fatalf("expected 3 shares, got %d", len(shares))
	}

	// 任意2个份额可以恢复
	combinations := [][]Share{
		{shares[0], shares[1]},
		{shares[0], shares[2]},
		{shares[1], shares[2]},
	}
	for i, combo := range combinations {
		recovered, err := Reconstruct(combo)
		if err != nil {
			t.Errorf("Reconstruct combo %d: %v", i, err)
			continue
		}
		if !bytes.Equal(recovered, secret) {
			t.Errorf("combo %d: recovered != secret", i)
		}
	}

	// 单个份额无法恢复（need at least 2 shares）
	_, err = Reconstruct([]Share{shares[0]})
	if err == nil {
		t.Error("expected error with only 1 share")
	}
}

// TestShamirReconstruct 验证恢复的秘密与原始一致。
func TestShamirReconstruct(t *testing.T) {
	secret := []byte("another-private-key-data-123456!")
	shares, err := Split(secret, 3, 5)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}

	// 使用前3个份额恢复
	recovered, err := Reconstruct(shares[:3])
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if !bytes.Equal(recovered, secret) {
		t.Errorf("recovered = %x, want %x", recovered, secret)
	}
}

// TestShamirDifferentSubsets 验证不同份额组合恢复的结果相同。
func TestShamirDifferentSubsets(t *testing.T) {
	secret := []byte("consistent-across-all-subsets!!!")
	shares, err := Split(secret, 3, 5)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}

	r1, err := Reconstruct([]Share{shares[0], shares[1], shares[2]})
	if err != nil {
		t.Fatalf("Reconstruct 1: %v", err)
	}
	r2, err := Reconstruct([]Share{shares[1], shares[3], shares[4]})
	if err != nil {
		t.Fatalf("Reconstruct 2: %v", err)
	}
	if !bytes.Equal(r1, r2) {
		t.Error("different subsets should recover the same secret")
	}
	if !bytes.Equal(r1, secret) {
		t.Errorf("recovered = %x, want %x", r1, secret)
	}
}

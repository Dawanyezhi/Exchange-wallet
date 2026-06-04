package main

import "testing"

func TestParseSampleBlockJSON(t *testing.T) {
	block, err := ParseBlockJSON([]byte(sampleBlockJSON))
	if err != nil {
		t.Fatal(err)
	}
	if block.TxCount != 1 || len(block.Transactions) != 1 {
		t.Fatalf("unexpected tx count: %+v", block)
	}
	tx := block.Transactions[0]
	if len(tx.SOLTransfers) != 1 {
		t.Fatalf("expected one SOL transfer: %+v", tx.SOLTransfers)
	}
	if tx.SOLTransfers[0].Lamports != 1_000_000_000 {
		t.Fatalf("lamports mismatch: %+v", tx.SOLTransfers[0])
	}
	if len(tx.BalanceDeltas) != 2 {
		t.Fatalf("expected two balance deltas: %+v", tx.BalanceDeltas)
	}
	if tx.Err != nil {
		t.Fatalf("expected successful tx, got err=%v", tx.Err)
	}
}

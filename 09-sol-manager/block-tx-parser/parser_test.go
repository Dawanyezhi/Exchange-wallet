package main

import (
	"os"
	"strings"
	"testing"
)

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

func TestParseFullRPCResponseAndRawTransactionJSON(t *testing.T) {
	raw := `{
	  "jsonrpc":"2.0",
	  "id":"test",
	  "result":{
	    "blockHeight":1,
	    "blockTime":2,
	    "blockhash":"7PtnQ1VxK6x9gHfK9x2q4GmqL7jN9tN1Yz1K4m7QYq3",
	    "parentSlot":0,
	    "previousBlockhash":"4vJ9JU1bJJE96FwsQ4Dq6T6zkxx7YxK7XhRZpYxPzQGp",
	    "transactions":[{
	      "version":"legacy",
	      "transaction":{
	        "signatures":["5j7s4VqZ6V6Q9b3Y5mDk8oD8m1qJkT7uQ9R9r5wV1zQ5f4d3s2a1p9m8n7b6v5c4x3z2a1s9d8f7g6h5j4k3"],
	        "message":{
	          "accountKeys":[
	            "3b6n97z5VgqqU4G4YfP7u5QmG2h76nHn8R7VwJ5fW6xk",
	            "Fj8tV9p5r3jYgS8xBQk9u1h6z7nQ7p9v3S6xXQ5h9k1B",
	            "11111111111111111111111111111111"
	          ],
	          "header":{"numRequiredSignatures":1,"numReadonlySignedAccounts":0,"numReadonlyUnsignedAccounts":1},
	          "recentBlockhash":"7PtnQ1VxK6x9gHfK9x2q4GmqL7jN9tN1Yz1K4m7QYq3",
	          "instructions":[{"programIdIndex":2,"accounts":[0,1],"data":"3Bxs4NN8M2Yn4TLb"}]
	        }
	      },
	      "meta":{
	        "err":null,
	        "fee":5000,
	        "preBalances":[5000000000,1000000,1],
	        "postBalances":[3999995000,1001000000,1],
	        "preTokenBalances":[],
	        "postTokenBalances":[],
	        "loadedAddresses":{"writable":[],"readonly":[]},
	        "innerInstructions":[]
	      }
	    }]
	  }
	}`

	block, err := ParseBlockJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if block.TxCount != 1 {
		t.Fatalf("unexpected tx count: %d", block.TxCount)
	}
	tx := block.Transactions[0]
	if len(tx.SOLTransfers) != 1 {
		t.Fatalf("expected one raw SOL transfer: %+v", tx.SOLTransfers)
	}
	if tx.SOLTransfers[0].Source != "raw_instruction" || tx.SOLTransfers[0].Lamports != 10_000_000 {
		t.Fatalf("unexpected transfer: %+v", tx.SOLTransfers[0])
	}
}

func TestTokenDeltasUseBigIntsAndCreditOnlySuccessfulPositiveDeltas(t *testing.T) {
	keys := []string{
		"3b6n97z5VgqqU4G4YfP7u5QmG2h76nHn8R7VwJ5fW6xk",
		"Fj8tV9p5r3jYgS8xBQk9u1h6z7nQ7p9v3S6xXQ5h9k1B",
	}
	pre := []TokenBalance{{
		AccountIndex: 1,
		Mint:         "TokenMint1111111111111111111111111111111111",
		ProgramID:    "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
		Owner:        "3b6n97z5VgqqU4G4YfP7u5QmG2h76nHn8R7VwJ5fW6xk",
		UITokenAmount: UITokenAmount{
			Amount:   "9223372036854775808",
			Decimals: 6,
		},
	}}
	post := []TokenBalance{{
		AccountIndex: 1,
		Mint:         "TokenMint1111111111111111111111111111111111",
		ProgramID:    "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
		Owner:        "3b6n97z5VgqqU4G4YfP7u5QmG2h76nHn8R7VwJ5fW6xk",
		UITokenAmount: UITokenAmount{
			Amount:   "9223372036854775818",
			Decimals: 6,
		},
	}}
	deltas := parseTokenDeltas(keys, pre, post)
	if len(deltas) != 1 {
		t.Fatalf("expected one delta: %+v", deltas)
	}
	if deltas[0].DeltaBaseUnit != "10" {
		t.Fatalf("unexpected delta: %+v", deltas[0])
	}

	failed := RPCTransaction{
		Transaction: EncodedTransaction{
			Signatures: []string{"5j7s4VqZ6V6Q9b3Y5mDk8oD8m1qJkT7uQ9R9r5wV1zQ5f4d3s2a1p9m8n7b6v5c4x3z2a1s9d8f7g6h5j4k3"},
			Message: RPCMessage{
				AccountKeys: AccountKeys{{Pubkey: keys[0]}, {Pubkey: keys[1]}},
			},
		},
		Meta: TransactionMeta{
			Err:               map[string]interface{}{"InstructionError": []interface{}{0, "Custom"}},
			PreTokenBalances:  pre,
			PostTokenBalances: post,
		},
	}
	summary, err := ParseTransaction(failed)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.TokenDeltas) != 1 {
		t.Fatalf("expected audit token delta on failed tx: %+v", summary.TokenDeltas)
	}
	if len(summary.CreditableTokenDeltas) != 0 {
		t.Fatalf("failed tx must not produce creditable deltas: %+v", summary.CreditableTokenDeltas)
	}
}

func TestParseMainnetProdNewJSON(t *testing.T) {
	raw, err := os.ReadFile("data/prod-new.json")
	if err != nil {
		t.Fatal(err)
	}
	block, err := ParseBlockJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if block.TxCount != 1482 || len(block.Transactions) != 1482 {
		t.Fatalf("unexpected prod-new tx count: count=%d parsed=%d", block.TxCount, len(block.Transactions))
	}
	if block.Blockhash == "" || block.PreviousBlockhash == "" {
		t.Fatalf("block hashes not parsed: %+v", block)
	}
	tokenDeltaCount := 0
	creditableCount := 0
	for _, tx := range block.Transactions {
		tokenDeltaCount += len(tx.TokenDeltas)
		creditableCount += len(tx.CreditableTokenDeltas)
	}
	if tokenDeltaCount == 0 {
		t.Fatal("expected token deltas in prod-new block")
	}
	if creditableCount == 0 {
		t.Fatal("expected creditable token deltas in prod-new block")
	}
}

func TestInstructionDataDecodeRejectsNonSystemTransfer(t *testing.T) {
	if _, ok := decodeSystemTransferLamports(""); ok {
		t.Fatal("empty data should not decode")
	}
	if _, ok := decodeSystemTransferLamports("1111"); ok {
		t.Fatal("short data should not decode")
	}
	if delta, err := decimalStringDelta("20", "10"); err != nil || !strings.HasPrefix(delta, "-") {
		t.Fatalf("expected negative big-int delta, got delta=%q err=%v", delta, err)
	}
}

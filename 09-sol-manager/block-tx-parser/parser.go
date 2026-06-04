package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
)

const (
	systemProgramID = "11111111111111111111111111111111"
)

type RPCBlock struct {
	BlockHeight       *uint64          `json:"blockHeight"`
	BlockTime         *int64           `json:"blockTime"`
	Blockhash         string           `json:"blockhash"`
	ParentSlot        uint64           `json:"parentSlot"`
	PreviousBlockhash string           `json:"previousBlockhash"`
	Transactions      []RPCTransaction `json:"transactions"`
}

type RPCTransaction struct {
	Transaction EncodedTransaction `json:"transaction"`
	Meta        TransactionMeta    `json:"meta"`
}

type EncodedTransaction struct {
	Signatures []string   `json:"signatures"`
	Message    RPCMessage `json:"message"`
}

type RPCMessage struct {
	AccountKeys     []AccountKey     `json:"accountKeys"`
	RecentBlockhash string           `json:"recentBlockhash"`
	Instructions    []RPCInstruction `json:"instructions"`
}

type AccountKey struct {
	Pubkey   string `json:"pubkey"`
	Signer   bool   `json:"signer"`
	Writable bool   `json:"writable"`
}

type RPCInstruction struct {
	ProgramID string             `json:"programId"`
	Program   string             `json:"program"`
	Type      string             `json:"type"`
	Parsed    *ParsedInstruction `json:"parsed"`
	Accounts  []string           `json:"accounts"`
	Data      string             `json:"data"`
}

type ParsedInstruction struct {
	Type string                 `json:"type"`
	Info map[string]interface{} `json:"info"`
}

type TransactionMeta struct {
	Err               interface{}    `json:"err"`
	Fee               uint64         `json:"fee"`
	PreBalances       []uint64       `json:"preBalances"`
	PostBalances      []uint64       `json:"postBalances"`
	PreTokenBalances  []TokenBalance `json:"preTokenBalances"`
	PostTokenBalances []TokenBalance `json:"postTokenBalances"`
}

type TokenBalance struct {
	AccountIndex  uint64        `json:"accountIndex"`
	Mint          string        `json:"mint"`
	Owner         string        `json:"owner"`
	ProgramID     string        `json:"programId"`
	UITokenAmount UITokenAmount `json:"uiTokenAmount"`
}

type UITokenAmount struct {
	Amount   string `json:"amount"`
	Decimals uint8  `json:"decimals"`
}

type ParsedBlockSummary struct {
	Blockhash         string
	PreviousBlockhash string
	BlockHeight       uint64
	BlockTime         int64
	TxCount           int
	Transactions      []ParsedTxSummary
}

type ParsedTxSummary struct {
	Signature     string
	Err           interface{}
	FeeLamports   uint64
	FeePayer      string
	AccountKeys   []string
	SOLTransfers  []SOLTransfer
	BalanceDeltas []BalanceDelta
	TokenDeltas   []TokenDelta
}

type SOLTransfer struct {
	From     string
	To       string
	Lamports int64
	Source   string
}

type BalanceDelta struct {
	Address       string
	PreLamports   uint64
	PostLamports  uint64
	DeltaLamports int64
}

type TokenDelta struct {
	Account       string
	Owner         string
	Mint          string
	ProgramID     string
	Decimals      uint8
	PreAmount     string
	PostAmount    string
	DeltaBaseUnit string
}

func ParseBlockJSON(raw []byte) (*ParsedBlockSummary, error) {
	var block RPCBlock
	if err := json.Unmarshal(raw, &block); err != nil {
		return nil, err
	}
	height := uint64(0)
	if block.BlockHeight != nil {
		height = *block.BlockHeight
	}
	out := &ParsedBlockSummary{
		Blockhash:         block.Blockhash,
		PreviousBlockhash: block.PreviousBlockhash,
		BlockHeight:       height,
		TxCount:           len(block.Transactions),
	}
	if block.BlockTime != nil {
		out.BlockTime = *block.BlockTime
	}
	for i := range block.Transactions {
		tx, err := ParseTransaction(block.Transactions[i])
		if err != nil {
			return nil, fmt.Errorf("parse tx[%d]: %w", i, err)
		}
		out.Transactions = append(out.Transactions, tx)
	}
	return out, nil
}

func ParseTransaction(tx RPCTransaction) (ParsedTxSummary, error) {
	if len(tx.Transaction.Signatures) == 0 {
		return ParsedTxSummary{}, errors.New("transaction missing signature")
	}
	keys := make([]string, 0, len(tx.Transaction.Message.AccountKeys))
	for _, key := range tx.Transaction.Message.AccountKeys {
		if !isValidPubkey(key.Pubkey) {
			return ParsedTxSummary{}, fmt.Errorf("invalid account key: %s", key.Pubkey)
		}
		keys = append(keys, key.Pubkey)
	}
	summary := ParsedTxSummary{
		Signature:   tx.Transaction.Signatures[0],
		Err:         tx.Meta.Err,
		FeeLamports: tx.Meta.Fee,
		AccountKeys: keys,
	}
	if len(keys) > 0 {
		summary.FeePayer = keys[0]
	}
	// SOL 主币充值/提现不能只看 instruction，还要结合 pre/postBalances 做余额差额对账。
	summary.BalanceDeltas = parseBalanceDeltas(keys, tx.Meta.PreBalances, tx.Meta.PostBalances)
	for _, ix := range tx.Transaction.Message.Instructions {
		if transfer, ok := parseSystemTransferInstruction(ix); ok {
			summary.SOLTransfers = append(summary.SOLTransfers, transfer)
		}
	}
	summary.TokenDeltas = parseTokenDeltas(keys, tx.Meta.PreTokenBalances, tx.Meta.PostTokenBalances)
	return summary, nil
}

func parseSystemTransferInstruction(ix RPCInstruction) (SOLTransfer, bool) {
	if ix.ProgramID != systemProgramID && ix.Program != "system" {
		return SOLTransfer{}, false
	}
	if ix.Parsed == nil || ix.Parsed.Type != "transfer" {
		return SOLTransfer{}, false
	}
	info := ix.Parsed.Info
	from := stringValue(info["source"])
	to := stringValue(info["destination"])
	lamports := int64Value(info["lamports"])
	if from == "" || to == "" || lamports <= 0 {
		return SOLTransfer{}, false
	}
	return SOLTransfer{From: from, To: to, Lamports: lamports, Source: "parsed_instruction"}, true
}

func parseBalanceDeltas(keys []string, pre []uint64, post []uint64) []BalanceDelta {
	limit := len(keys)
	if len(pre) < limit {
		limit = len(pre)
	}
	if len(post) < limit {
		limit = len(post)
	}
	out := make([]BalanceDelta, 0)
	for i := 0; i < limit; i++ {
		delta := int64(post[i]) - int64(pre[i])
		if delta == 0 {
			continue
		}
		out = append(out, BalanceDelta{
			Address:       keys[i],
			PreLamports:   pre[i],
			PostLamports:  post[i],
			DeltaLamports: delta,
		})
	}
	return out
}

func parseTokenDeltas(keys []string, pre []TokenBalance, post []TokenBalance) []TokenDelta {
	type tokenKey struct {
		AccountIndex uint64
		Mint         string
		ProgramID    string
	}
	preMap := make(map[tokenKey]TokenBalance)
	postMap := make(map[tokenKey]TokenBalance)
	keySet := make(map[tokenKey]bool)
	for _, b := range pre {
		k := tokenKey{AccountIndex: b.AccountIndex, Mint: b.Mint, ProgramID: b.ProgramID}
		preMap[k] = b
		keySet[k] = true
	}
	for _, b := range post {
		k := tokenKey{AccountIndex: b.AccountIndex, Mint: b.Mint, ProgramID: b.ProgramID}
		postMap[k] = b
		keySet[k] = true
	}
	var keysSorted []tokenKey
	for k := range keySet {
		keysSorted = append(keysSorted, k)
	}
	sort.Slice(keysSorted, func(i, j int) bool {
		if keysSorted[i].AccountIndex == keysSorted[j].AccountIndex {
			return keysSorted[i].Mint < keysSorted[j].Mint
		}
		return keysSorted[i].AccountIndex < keysSorted[j].AccountIndex
	})
	out := make([]TokenDelta, 0)
	for _, k := range keysSorted {
		before := preMap[k]
		after := postMap[k]
		preAmount := before.UITokenAmount.Amount
		postAmount := after.UITokenAmount.Amount
		if preAmount == "" {
			preAmount = "0"
		}
		if postAmount == "" {
			postAmount = "0"
		}
		delta, err := decimalStringDelta(preAmount, postAmount)
		if err != nil || delta == "0" {
			continue
		}
		account := ""
		if int(k.AccountIndex) < len(keys) {
			account = keys[k.AccountIndex]
		}
		owner := after.Owner
		if owner == "" {
			owner = before.Owner
		}
		decimals := after.UITokenAmount.Decimals
		if decimals == 0 {
			decimals = before.UITokenAmount.Decimals
		}
		out = append(out, TokenDelta{
			Account:       account,
			Owner:         owner,
			Mint:          k.Mint,
			ProgramID:     k.ProgramID,
			Decimals:      decimals,
			PreAmount:     preAmount,
			PostAmount:    postAmount,
			DeltaBaseUnit: delta,
		})
	}
	return out
}

func stringValue(v interface{}) string {
	s, _ := v.(string)
	return s
}

func int64Value(v interface{}) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	default:
		return 0
	}
}

func decimalStringDelta(pre string, post string) (string, error) {
	a, err := strconv.ParseInt(pre, 10, 64)
	if err != nil {
		return "", err
	}
	b, err := strconv.ParseInt(post, 10, 64)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(b-a, 10), nil
}

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
)

const (
	systemProgramID = "11111111111111111111111111111111"
)

type RPCBlock struct {
	BlockHeight       *uint64          `json:"blockHeight"`       // Solana block height，和 slot 不是同一个概念，RPC 可能返回 null。
	BlockTime         *int64           `json:"blockTime"`         // 链上区块时间戳，RPC 可能返回 null。
	Blockhash         string           `json:"blockhash"`         // 当前真实产出 block 的 hash。
	ParentSlot        uint64           `json:"parentSlot"`        // 父 block 所在 slot，不一定等于当前 slot - 1。
	PreviousBlockhash string           `json:"previousBlockhash"` // 父 block hash，用于校验链式关系。
	Transactions      []RPCTransaction `json:"transactions"`      // 当前 block 内的交易列表。
}

type RPCTransaction struct {
	Transaction EncodedTransaction `json:"transaction"` // 交易 message 和 signature。
	Meta        TransactionMeta    `json:"meta"`        // 执行结果、手续费、余额变化和 inner instruction。
	Version     interface{}        `json:"version"`     // legacy 为字符串，v0 交易通常为数字 0。
}

type EncodedTransaction struct {
	Signatures []string   `json:"signatures"` // signatures[0] 是 Solana 交易 ID。
	Message    RPCMessage `json:"message"`    // 交易消息体，包含账户、blockhash 和指令。
}

type RPCMessage struct {
	AccountKeys     AccountKeys      `json:"accountKeys"`     // 静态账户列表；v0 交易还要追加 meta.loadedAddresses。
	Header          MessageHeader    `json:"header"`          // 签名账户和只读账户数量，用于还原账户权限。
	RecentBlockhash string           `json:"recentBlockhash"` // 构建交易时使用的 recent blockhash。
	Instructions    []RPCInstruction `json:"instructions"`    // 外层 instruction 列表。
}

// AccountKeys 同时兼容 encoding=json 的字符串数组和 encoding=jsonParsed 的对象数组。
type AccountKeys []AccountKey

type AccountKey struct {
	Pubkey   string `json:"pubkey"`   // 账户地址或 program id。
	Signer   bool   `json:"signer"`   // 是否需要签名；jsonParsed 才会直接给出。
	Writable bool   `json:"writable"` // 是否可写；jsonParsed 才会直接给出。
}

type RPCInstruction struct {
	ProgramID      string             `json:"programId"`      // jsonParsed 结构中直接给出的 program id。
	Program        string             `json:"program"`        // jsonParsed 结构中的 program 名称，例如 system。
	Type           string             `json:"type"`           // 少数 parsed 结构会把类型放在外层。
	Parsed         *ParsedInstruction `json:"parsed"`         // jsonParsed instruction 的已解析字段。
	Accounts       AccountReferences  `json:"accounts"`       // raw 为账户下标数组；parsed/SDK 结构可能是地址数组。
	Data           string             `json:"data"`           // raw instruction data，base58 编码。
	ProgramIDIndex *uint64            `json:"programIdIndex"` // raw 结构中 program id 在 resolved account keys 里的下标。
	StackHeight    *uint64            `json:"stackHeight"`    // CPI 调用栈高度，部分 RPC 返回。
}

type ParsedInstruction struct {
	Type string                 `json:"type"` // parsed instruction 类型，例如 transfer。
	Info map[string]interface{} `json:"info"` // parsed instruction 的业务字段。
}

type AccountReferences struct {
	Addresses []string // 已解析成地址的账户引用。
	Indexes   []uint64 // raw instruction 中指向 resolved account keys 的账户下标。
}

type MessageHeader struct {
	NumRequiredSignatures       uint64 `json:"numRequiredSignatures"`       // 前 N 个 account keys 是 signer。
	NumReadonlySignedAccounts   uint64 `json:"numReadonlySignedAccounts"`   // signer 中只读账户数量。
	NumReadonlyUnsignedAccounts uint64 `json:"numReadonlyUnsignedAccounts"` // 非 signer 中只读账户数量。
}

type TransactionMeta struct {
	Err               interface{}         `json:"err"`               // 非空表示交易执行失败，不能入账。
	Fee               uint64              `json:"fee"`               // 实际手续费，单位 lamports。
	InnerInstructions []InnerInstructions `json:"innerInstructions"` // CPI 产生的 inner instruction。
	LoadedAddresses   LoadedAddresses     `json:"loadedAddresses"`   // v0 address lookup table 加载出的账户。
	PreBalances       []uint64            `json:"preBalances"`       // 交易前 SOL 余额，按 resolved account keys 下标对应。
	PostBalances      []uint64            `json:"postBalances"`      // 交易后 SOL 余额，按 resolved account keys 下标对应。
	PreTokenBalances  []TokenBalance      `json:"preTokenBalances"`  // 交易前 Token 余额快照。
	PostTokenBalances []TokenBalance      `json:"postTokenBalances"` // 交易后 Token 余额快照。
}

type InnerInstructions struct {
	Index        uint64           `json:"index"`        // 对应外层 instruction_index。
	Instructions []RPCInstruction `json:"instructions"` // 该外层 instruction 触发的 CPI instruction 列表。
}

type LoadedAddresses struct {
	Writable []string `json:"writable"` // v0 交易通过 address lookup table 加载出的可写账户。
	Readonly []string `json:"readonly"` // v0 交易通过 address lookup table 加载出的只读账户。
}

type TokenBalance struct {
	AccountIndex  uint64        `json:"accountIndex"`  // token account 在 resolved account keys 中的下标。
	Mint          string        `json:"mint"`          // Token mint address，资产白名单必须用它校验。
	Owner         string        `json:"owner"`         // token account 背后的 owner 钱包地址。
	ProgramID     string        `json:"programId"`     // Token Program 或 Token-2022 Program id。
	UITokenAmount UITokenAmount `json:"uiTokenAmount"` // Token 金额，入账只使用 amount 字符串。
}

type UITokenAmount struct {
	Amount   string `json:"amount"`   // base units 字符串，不能转 float。
	Decimals uint8  `json:"decimals"` // mint decimals，用于和白名单配置交叉校验。
}

type ParsedBlockSummary struct {
	Blockhash         string            // 当前 block hash。
	PreviousBlockhash string            // 父 block hash。
	BlockHeight       uint64            // block height，RPC 为空时为 0。
	BlockTime         int64             // block time，RPC 为空时为 0。
	TxCount           int               // block 中交易数量。
	Transactions      []ParsedTxSummary // 逐笔交易解析摘要。
}

type ParsedTxSummary struct {
	Signature             string         // Solana 交易签名，等价交易 ID。
	Err                   interface{}    // 链上执行错误；非空不能入账。
	FeeLamports           uint64         // 实际手续费，单位 lamports。
	FeePayer              string         // fee payer，通常是 resolved account keys[0]。
	AccountKeys           []string       // 静态账户 + v0 loaded addresses 后的 resolved account keys。
	SOLTransfers          []SOLTransfer  // 已解码出的 SOL transfer instruction，作为辅助审计字段。
	BalanceDeltas         []BalanceDelta // SOL 余额差额，主币充值/提现应优先参考这个字段。
	TokenDeltas           []TokenDelta   // Token 余额差额审计字段，失败交易也可能保留。
	CreditableTokenDeltas []TokenDelta   // 成功交易里 delta>0 的 Token 入账候选，仍需白名单和归属校验。
}

type SOLTransfer struct {
	From     string // 转出地址。
	To       string // 转入地址。
	Lamports int64  // 转账 lamports。
	Source   string // 来源：parsed_instruction、raw_instruction 或 inner_instruction_N。
}

type BalanceDelta struct {
	Address       string // SOL 余额变化对应账户。
	PreLamports   uint64 // 交易前 lamports。
	PostLamports  uint64 // 交易后 lamports。
	DeltaLamports int64  // post - pre；fee payer 通常为负数。
}

type TokenDelta struct {
	Account       string // token account 地址。
	Owner         string // token account owner。
	Mint          string // mint address。
	ProgramID     string // token program id。
	Decimals      uint8  // mint decimals。
	PreAmount     string // 交易前 base units 字符串。
	PostAmount    string // 交易后 base units 字符串。
	DeltaBaseUnit string // post - pre，base units 字符串。
}

// ParseBlockJSON 的流程：
// 1. 兼容完整 JSON-RPC 响应和 result block 两种输入。
// 2. 解析 block 元数据。
// 3. 逐笔调用 ParseTransaction，任何一笔结构异常都会返回带 tx index 的错误。
func ParseBlockJSON(raw []byte) (*ParsedBlockSummary, error) {
	// 本地样本可能是完整 JSON-RPC 响应，也可能已经是 result 里的 block；先统一成 block JSON。
	raw = unwrapRPCResult(raw)
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

// ParseTransaction 的流程：
// 1. 校验 signature，并解析静态账户 + v0 loaded addresses。
// 2. 根据 pre/postBalances 计算 SOL 差额。
// 3. 解码已支持的 outer/inner instruction。
// 4. 根据 pre/postTokenBalances 计算 Token 差额。
// 5. 仅成功交易的正向 Token delta 才进入可入账候选。
func ParseTransaction(tx RPCTransaction) (ParsedTxSummary, error) {
	if len(tx.Transaction.Signatures) == 0 {
		return ParsedTxSummary{}, errors.New("transaction missing signature")
	}
	// 官方 JSON 结构里的 accountKeys 只是静态账户；v0 交易还要追加 loadedAddresses 后才能解析 account index。
	keys, err := resolveAccountKeys(tx)
	if err != nil {
		return ParsedTxSummary{}, err
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
	// raw JSON instruction 需要通过 programIdIndex/accounts 下标还原 program 和账户；jsonParsed 则直接读 parsed.info。
	for _, ix := range tx.Transaction.Message.Instructions {
		if transfer, ok := parseSystemTransferInstruction(ix, keys); ok {
			summary.SOLTransfers = append(summary.SOLTransfers, transfer)
		}
	}
	// CPI 产生的 inner instruction 也可能包含 SOL/Token 动作，不能只看外层 instruction。
	for _, inner := range tx.Meta.InnerInstructions {
		for _, ix := range inner.Instructions {
			if transfer, ok := parseSystemTransferInstruction(ix, keys); ok {
				transfer.Source = fmt.Sprintf("inner_instruction_%d", inner.Index)
				summary.SOLTransfers = append(summary.SOLTransfers, transfer)
			}
		}
	}
	// SPL Token 充值优先以 meta 的 token balance delta 为准，后续再做 mint 白名单和 token account 归属校验。
	summary.TokenDeltas = parseTokenDeltas(keys, tx.Meta.PreTokenBalances, tx.Meta.PostTokenBalances)
	if summary.Err == nil {
		// 失败交易保留 TokenDeltas 做审计，但不会生成可入账候选。
		for _, delta := range summary.TokenDeltas {
			if len(delta.DeltaBaseUnit) > 0 && delta.DeltaBaseUnit[0] != '-' {
				summary.CreditableTokenDeltas = append(summary.CreditableTokenDeltas, delta)
			}
		}
	}
	return summary, nil
}

// parseSystemTransferInstruction 同时支持 jsonParsed transfer 和 raw System Program transfer。
// 生产主币入账仍以 BalanceDeltas 命中我方地址为准，instruction 解码只做辅助审计。
func parseSystemTransferInstruction(ix RPCInstruction, keys []string) (SOLTransfer, bool) {
	programID := resolveProgramID(ix, keys)
	if programID != systemProgramID && ix.Program != "system" {
		return SOLTransfer{}, false
	}
	if ix.Parsed != nil && ix.Parsed.Type == "transfer" {
		info := ix.Parsed.Info
		from := stringValue(info["source"])
		to := stringValue(info["destination"])
		lamports := int64Value(info["lamports"])
		if from == "" || to == "" || lamports <= 0 {
			return SOLTransfer{}, false
		}
		return SOLTransfer{From: from, To: to, Lamports: lamports, Source: "parsed_instruction"}, true
	}
	if len(ix.Accounts.Indexes) < 2 || ix.Data == "" {
		return SOLTransfer{}, false
	}
	// System transfer 的 raw data 是 instruction id + lamports，小端编码。
	lamports, ok := decodeSystemTransferLamports(ix.Data)
	if !ok {
		return SOLTransfer{}, false
	}
	from := accountAt(keys, ix.Accounts.Indexes[0])
	to := accountAt(keys, ix.Accounts.Indexes[1])
	if from == "" || to == "" {
		return SOLTransfer{}, false
	}
	return SOLTransfer{From: from, To: to, Lamports: lamports, Source: "raw_instruction"}, true
}

// parseBalanceDeltas 按 resolved account keys 下标对齐 pre/postBalances。
// Solana fee 会体现在 fee payer 的负向 delta 中，不能误当用户转出。
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

// parseTokenDeltas 按 accountIndex + mint + programId 配对 pre/postTokenBalances。
// 这里只计算余额变化；生产入账还必须校验 mint 白名单、programId、decimals 和 token account 归属。
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
		// uiTokenAmount.amount 是 base units 字符串，不能用 uiAmount，也不能转 float。
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
	// SPL Token base units 可能超过 int64，统一用大整数做差。
	a, ok := new(big.Int).SetString(pre, 10)
	if !ok {
		return "", fmt.Errorf("invalid integer %q", pre)
	}
	b, ok := new(big.Int).SetString(post, 10)
	if !ok {
		return "", fmt.Errorf("invalid integer %q", post)
	}
	return new(big.Int).Sub(b, a).String(), nil
}

func unwrapRPCResult(raw []byte) []byte {
	// RPC 在线调用返回的已经是 result；手工保存的文件常常保留 jsonrpc/id/result 外壳。
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return raw
	}
	if len(envelope.Result) == 0 || bytes.Equal(envelope.Result, []byte("null")) {
		return raw
	}
	return envelope.Result
}

func (keys *AccountKeys) UnmarshalJSON(raw []byte) error {
	// encoding=json 返回字符串数组；encoding=jsonParsed 返回带 signer/writable 的对象数组。
	var strings []string
	if err := json.Unmarshal(raw, &strings); err == nil {
		out := make([]AccountKey, 0, len(strings))
		for i, pubkey := range strings {
			out = append(out, AccountKey{
				Pubkey: pubkey,
				Signer: i == 0,
			})
		}
		*keys = out
		return nil
	}
	var objects []AccountKey
	if err := json.Unmarshal(raw, &objects); err != nil {
		return err
	}
	*keys = objects
	return nil
}

func (refs *AccountReferences) UnmarshalJSON(raw []byte) error {
	// raw instruction 的 accounts 是下标数组；jsonParsed/部分 SDK 结构可能已经是地址数组。
	var indexes []uint64
	if err := json.Unmarshal(raw, &indexes); err == nil {
		refs.Indexes = indexes
		return nil
	}
	var addresses []string
	if err := json.Unmarshal(raw, &addresses); err != nil {
		return err
	}
	refs.Addresses = addresses
	return nil
}

func resolveAccountKeys(tx RPCTransaction) ([]string, error) {
	// v0 transaction 的 programIdIndex/accountIndex 可能指向 loadedAddresses，必须拼接后再解析。
	keys := make([]string, 0, len(tx.Transaction.Message.AccountKeys)+len(tx.Meta.LoadedAddresses.Writable)+len(tx.Meta.LoadedAddresses.Readonly))
	for _, key := range tx.Transaction.Message.AccountKeys {
		if !isValidPubkey(key.Pubkey) {
			return nil, fmt.Errorf("invalid account key: %s", key.Pubkey)
		}
		keys = append(keys, key.Pubkey)
	}
	for _, pubkey := range tx.Meta.LoadedAddresses.Writable {
		if !isValidPubkey(pubkey) {
			return nil, fmt.Errorf("invalid writable loaded address: %s", pubkey)
		}
		keys = append(keys, pubkey)
	}
	for _, pubkey := range tx.Meta.LoadedAddresses.Readonly {
		if !isValidPubkey(pubkey) {
			return nil, fmt.Errorf("invalid readonly loaded address: %s", pubkey)
		}
		keys = append(keys, pubkey)
	}
	return keys, nil
}

func resolveProgramID(ix RPCInstruction, keys []string) string {
	if ix.ProgramID != "" {
		return ix.ProgramID
	}
	if ix.ProgramIDIndex == nil {
		return ""
	}
	return accountAt(keys, *ix.ProgramIDIndex)
}

func accountAt(keys []string, index uint64) string {
	if index >= uint64(len(keys)) {
		return ""
	}
	return keys[index]
}

func decodeSystemTransferLamports(data string) (int64, bool) {
	raw, err := decodeBase58(data)
	if err != nil {
		return 0, false
	}
	if len(raw) < 12 {
		return 0, false
	}
	if binary.LittleEndian.Uint32(raw[:4]) != 2 {
		return 0, false
	}
	lamports := binary.LittleEndian.Uint64(raw[4:12])
	if lamports > uint64(^uint64(0)>>1) {
		return 0, false
	}
	return int64(lamports), true
}

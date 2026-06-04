package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"

	"github.com/ethereum/go-ethereum/crypto"
)

const (
	sighashAll       byte   = 0x01
	rbfSequence      uint32 = 0xfffffffd
	minChangeDustSat int64  = 546
	mainnetHRP              = "bc"
)

type UTXO struct {
	PrevTxID       string
	PrevVout       uint32
	AmountSat      int64
	Address        string
	ScriptPubKey   []byte
	DerivationPath string
	CompressedPub  []byte
	PrivateKey     *hdPrivateKey
}

type TxOutput struct {
	Address      string
	AmountSat    int64
	Purpose      string
	ScriptPubKey []byte
}

type SpendPolicy struct {
	NetworkHRP              string
	MaxFeeSat               int64
	MaxFeeRateSatVB         int64
	RequireChangeOutput     bool
	RequireChangeOwned      bool
	AllowedChangeAddresses  map[string]bool
	AllowedOutputPurposes   map[string]bool
	ExpectedWithdrawAddress string
	ExpectedWithdrawSat     int64
}

type SignInput struct {
	Index          int
	PrevTxID       string
	PrevVout       uint32
	AmountSat      int64
	ScriptPubKey   []byte
	ScriptCode     []byte
	DerivationPath string
	ExpectedPubKey []byte
	PrivateKey     *hdPrivateKey
}

type SignRequest struct {
	RequestID   string
	NetworkHRP  string
	TxVersion   int32
	LockTime    uint32
	SighashType byte
	Tx          *MsgTx
	Inputs      []SignInput
	Outputs     []TxOutput
	Policy      SpendPolicy
}

type SignResponse struct {
	RequestID    string
	Approved     bool
	Signatures   []InputSignature
	SignerAudit  string
	RejectReason string
}

type InputSignature struct {
	InputIndex      int
	Signature       []byte
	CompressedPub   []byte
	DerivationPath  string
	ComputedSighash string
}

type SignedBTCTransaction struct {
	RawTxHex string
	TxID     string
	WTxID    string
	VSize    int
	FeeSat   int64
	Outputs  []SignedOutput
}

type SignedOutput struct {
	Index           uint32
	AmountSat       int64
	ScriptPubKeyHex string
	Address         string
	Type            string
}

type MsgTx struct {
	Version  int32
	Inputs   []TxIn
	Outputs  []TxOut
	LockTime uint32
}

type TxIn struct {
	PrevTxID       [32]byte // 内部序列化使用小端 tx hash。
	PrevTxIDString string
	PrevVout       uint32
	ScriptSig      []byte
	Sequence       uint32
	Witness        [][]byte
}

type TxOut struct {
	ValueSat     int64
	ScriptPubKey []byte
}

func BuildP2WPKHSpend(requestID string, inputs []UTXO, withdrawTo string, withdrawSat, feeRateSatVB int64, changeAddress string, changePriv *hdPrivateKey) (*SignRequest, error) {
	if len(inputs) == 0 {
		return nil, errors.New("no utxo selected")
	}
	if withdrawSat <= 0 || feeRateSatVB <= 0 {
		return nil, errors.New("withdraw amount and fee rate must be positive")
	}
	withdrawScript, err := scriptForAddress(withdrawTo, mainnetHRP)
	if err != nil {
		return nil, fmt.Errorf("withdraw address: %w", err)
	}
	changeScript, err := scriptForAddress(changeAddress, mainnetHRP)
	if err != nil {
		return nil, fmt.Errorf("change address: %w", err)
	}

	inputSum := int64(0)
	for _, in := range inputs {
		if in.AmountSat <= 0 {
			return nil, fmt.Errorf("input %s:%d amount must be positive", in.PrevTxID, in.PrevVout)
		}
		inputSum += in.AmountSat
	}
	// BTC 没有账户余额扣减，手续费来自输入和输出的差额；生产必须先按 vsize 估算费用，再决定是否生成找零输出。
	feeWithChange := estimateP2WPKHVSize(len(inputs), 2) * feeRateSatVB
	changeSat := inputSum - withdrawSat - feeWithChange
	outputs := []TxOutput{{
		Address:      withdrawTo,
		AmountSat:    withdrawSat,
		Purpose:      "withdraw_target",
		ScriptPubKey: withdrawScript,
	}}
	if changeSat >= minChangeDustSat {
		outputs = append(outputs, TxOutput{
			Address:      changeAddress,
			AmountSat:    changeSat,
			Purpose:      "system_change",
			ScriptPubKey: changeScript,
		})
	} else {
		// 找零低于 dust 时不生成找零 UTXO，否则后续花费成本可能高于该 UTXO 价值。
		feeNoChange := estimateP2WPKHVSize(len(inputs), 1) * feeRateSatVB
		if inputSum-withdrawSat-feeNoChange < 0 {
			return nil, fmt.Errorf("insufficient input: input=%d withdraw=%d estimated_fee=%d", inputSum, withdrawSat, feeNoChange)
		}
		changeSat = 0
	}

	tx := &MsgTx{Version: 2, LockTime: 0}
	signInputs := make([]SignInput, 0, len(inputs))
	for i, in := range inputs {
		prev, err := txIDStringToLittleEndian(in.PrevTxID)
		if err != nil {
			return nil, err
		}
		pubHash := extractP2WPKHPubKeyHash(in.ScriptPubKey)
		if pubHash == nil {
			return nil, fmt.Errorf("input %d script is not P2WPKH: %x", i, in.ScriptPubKey)
		}
		// BIP143 的签名摘要必须带上被花费 prevout 的金额和 scriptCode，签名机不能只签在线服务传来的 32 字节 hash。
		scriptCode, err := p2wpkhScriptCode(pubHash)
		if err != nil {
			return nil, err
		}
		tx.Inputs = append(tx.Inputs, TxIn{
			PrevTxID:       prev,
			PrevTxIDString: in.PrevTxID,
			PrevVout:       in.PrevVout,
			Sequence:       rbfSequence,
		})
		signInputs = append(signInputs, SignInput{
			Index:          i,
			PrevTxID:       in.PrevTxID,
			PrevVout:       in.PrevVout,
			AmountSat:      in.AmountSat,
			ScriptPubKey:   append([]byte(nil), in.ScriptPubKey...),
			ScriptCode:     scriptCode,
			DerivationPath: in.DerivationPath,
			ExpectedPubKey: append([]byte(nil), in.CompressedPub...),
			PrivateKey:     in.PrivateKey,
		})
	}
	for _, out := range outputs {
		tx.Outputs = append(tx.Outputs, TxOut{ValueSat: out.AmountSat, ScriptPubKey: out.ScriptPubKey})
	}
	policy := SpendPolicy{
		NetworkHRP:              mainnetHRP,
		MaxFeeSat:               50_000,
		MaxFeeRateSatVB:         100,
		RequireChangeOutput:     len(outputs) == 2,
		RequireChangeOwned:      true,
		AllowedChangeAddresses:  map[string]bool{changeAddress: true},
		AllowedOutputPurposes:   map[string]bool{"withdraw_target": true, "system_change": true},
		ExpectedWithdrawAddress: withdrawTo,
		ExpectedWithdrawSat:     withdrawSat,
	}
	req := &SignRequest{
		RequestID:   requestID,
		NetworkHRP:  mainnetHRP,
		TxVersion:   tx.Version,
		LockTime:    tx.LockTime,
		SighashType: sighashAll,
		Tx:          tx,
		Inputs:      signInputs,
		Outputs:     outputs,
		Policy:      policy,
	}
	if err := ValidateUnsignedSpend(req); err != nil {
		return nil, err
	}
	_ = changePriv // 生产中找零私钥留在签名域，这里只校验找零地址归属表。
	return req, nil
}

func OfflineSignP2WPKH(req *SignRequest) (*SignResponse, error) {
	if err := ValidateUnsignedSpend(req); err != nil {
		return &SignResponse{RequestID: req.RequestID, Approved: false, RejectReason: err.Error()}, err
	}
	resp := &SignResponse{
		RequestID:   req.RequestID,
		Approved:    true,
		SignerAudit: "btc-hot-hsm-demo-000001",
	}
	for _, in := range req.Inputs {
		pub, err := in.PrivateKey.compressedPubKey()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(pub, in.ExpectedPubKey) {
			return nil, fmt.Errorf("input %d expected pubkey mismatch", in.Index)
		}
		// 签名机二次确认派生公钥确实锁定了该 UTXO，避免在线服务把别人的 prevout 塞进待签交易。
		pubHash := hash160(pub)
		expectedScript, _ := p2wpkhScript(pubHash)
		if !bytes.Equal(expectedScript, in.ScriptPubKey) {
			return nil, fmt.Errorf("input %d scriptPubKey does not match signer pubkey", in.Index)
		}
		expectedScriptCode, _ := p2wpkhScriptCode(pubHash)
		if !bytes.Equal(expectedScriptCode, in.ScriptCode) {
			return nil, fmt.Errorf("input %d scriptCode does not match scriptPubKey", in.Index)
		}
		sighash := calcBIP143Sighash(req.Tx, in.Index, in.ScriptCode, in.AmountSat, req.SighashType)
		priv, err := in.PrivateKey.privateKey()
		if err != nil {
			return nil, err
		}
		sig, err := crypto.Sign(sighash[:], priv)
		if err != nil {
			return nil, err
		}
		der, err := compactSigToDER(sig)
		if err != nil {
			return nil, err
		}
		der = append(der, req.SighashType)
		resp.Signatures = append(resp.Signatures, InputSignature{
			InputIndex:      in.Index,
			Signature:       der,
			CompressedPub:   pub,
			DerivationPath:  in.DerivationPath,
			ComputedSighash: hex.EncodeToString(sighash[:]),
		})
	}
	return resp, nil
}

func AssembleAndVerify(req *SignRequest, resp *SignResponse) (*SignedBTCTransaction, error) {
	if resp == nil || !resp.Approved {
		return nil, fmt.Errorf("sign request rejected: %s", resp.RejectReason)
	}
	tx := req.Tx.copy()
	for _, sig := range resp.Signatures {
		if sig.InputIndex < 0 || sig.InputIndex >= len(tx.Inputs) {
			return nil, fmt.Errorf("signature index out of range: %d", sig.InputIndex)
		}
		tx.Inputs[sig.InputIndex].Witness = [][]byte{sig.Signature, sig.CompressedPub}
	}
	// 签后反解析和验签是广播前最后一道防线：确认 witness 能花费本地记录的 prevout，且输出没有被替换。
	if err := VerifySignedSpend(tx, req); err != nil {
		return nil, err
	}
	raw := tx.serialize(true)
	rawNoWitness := tx.serialize(false)
	txid := displayHash(doubleSHA256(rawNoWitness))
	wtxid := displayHash(doubleSHA256(raw))
	vsize := virtualSize(len(rawNoWitness), len(raw))
	inputSum, outputSum := req.inputOutputSums()
	return &SignedBTCTransaction{
		RawTxHex: hex.EncodeToString(raw),
		TxID:     txid,
		WTxID:    wtxid,
		VSize:    vsize,
		FeeSat:   inputSum - outputSum,
		Outputs:  parseSignedOutputs(tx, req.NetworkHRP),
	}, nil
}

func ValidateUnsignedSpend(req *SignRequest) error {
	if req == nil || req.Tx == nil {
		return errors.New("empty sign request")
	}
	if len(req.Inputs) != len(req.Tx.Inputs) {
		return fmt.Errorf("input count mismatch: sign=%d tx=%d", len(req.Inputs), len(req.Tx.Inputs))
	}
	if len(req.Outputs) != len(req.Tx.Outputs) {
		return fmt.Errorf("output count mismatch: sign=%d tx=%d", len(req.Outputs), len(req.Tx.Outputs))
	}
	inputSum, outputSum := req.inputOutputSums()
	fee := inputSum - outputSum
	// 金额守恒是 UTXO 交易最核心的风控校验：手续费只能等于输入总额减输出总额。
	if inputSum <= 0 || fee < 0 {
		return fmt.Errorf("invalid input/output sums: input=%d output=%d", inputSum, outputSum)
	}
	if req.Policy.MaxFeeSat > 0 && fee > req.Policy.MaxFeeSat {
		return fmt.Errorf("fee %d exceeds max_fee_sat %d", fee, req.Policy.MaxFeeSat)
	}
	estimatedVSize := estimateP2WPKHVSize(len(req.Tx.Inputs), len(req.Tx.Outputs))
	if req.Policy.MaxFeeRateSatVB > 0 && fee/int64(estimatedVSize) > req.Policy.MaxFeeRateSatVB {
		return fmt.Errorf("fee rate too high: fee=%d estimated_vsize=%d", fee, estimatedVSize)
	}
	for _, out := range req.Outputs {
		if !req.Policy.AllowedOutputPurposes[out.Purpose] {
			return fmt.Errorf("output purpose %q is not allowed", out.Purpose)
		}
		if out.AmountSat <= 0 {
			return fmt.Errorf("output %s amount must be positive", out.Purpose)
		}
		if out.Purpose == "withdraw_target" {
			if out.Address != req.Policy.ExpectedWithdrawAddress || out.AmountSat != req.Policy.ExpectedWithdrawSat {
				return fmt.Errorf("withdraw output mismatch: %s %d", out.Address, out.AmountSat)
			}
		}
		if out.Purpose == "system_change" && req.Policy.RequireChangeOwned && !req.Policy.AllowedChangeAddresses[out.Address] {
			// 找零必须回到系统登记的 change=1 地址，不能由提现请求方直接指定。
			return fmt.Errorf("change address is not owned by system: %s", out.Address)
		}
	}
	if req.Policy.RequireChangeOutput && len(req.Outputs) < 2 {
		return errors.New("change output is required by policy")
	}
	for _, in := range req.Inputs {
		if in.Index < 0 || in.Index >= len(req.Tx.Inputs) {
			return fmt.Errorf("input index out of range: %d", in.Index)
		}
		txIn := req.Tx.Inputs[in.Index]
		if txIn.PrevTxIDString != in.PrevTxID || txIn.PrevVout != in.PrevVout {
			return fmt.Errorf("input %d outpoint mismatch", in.Index)
		}
		if extractP2WPKHPubKeyHash(in.ScriptPubKey) == nil {
			return fmt.Errorf("input %d is not P2WPKH", in.Index)
		}
	}
	return nil
}

func VerifySignedSpend(tx *MsgTx, req *SignRequest) error {
	if err := ValidateUnsignedSpend(req); err != nil {
		return err
	}
	for _, in := range req.Inputs {
		if len(tx.Inputs[in.Index].ScriptSig) != 0 {
			return fmt.Errorf("input %d scriptSig must be empty for native P2WPKH", in.Index)
		}
		wit := tx.Inputs[in.Index].Witness
		if len(wit) != 2 {
			return fmt.Errorf("input %d witness stack must have signature and pubkey", in.Index)
		}
		if !bytes.Equal(wit[1], in.ExpectedPubKey) {
			return fmt.Errorf("input %d witness pubkey mismatch", in.Index)
		}
		if len(wit[0]) == 0 || wit[0][len(wit[0])-1] != sighashAll {
			return fmt.Errorf("input %d only SIGHASH_ALL is accepted", in.Index)
		}
		// 使用本地 prevout 金额重新计算 BIP143 sighash，防止签名服务或在线服务篡改 amount/scriptCode。
		sighash := calcBIP143Sighash(tx, in.Index, in.ScriptCode, in.AmountSat, sighashAll)
		if !verifyDERSignature(wit[0][:len(wit[0])-1], sighash[:], wit[1]) {
			return fmt.Errorf("input %d signature verification failed", in.Index)
		}
	}
	return nil
}

func calcBIP143Sighash(tx *MsgTx, inputIndex int, scriptCode []byte, amountSat int64, hashType byte) [32]byte {
	var pre bytes.Buffer
	pre.Write(int32LE(tx.Version))
	pre.Write(hashPrevouts(tx))
	pre.Write(hashSequence(tx))
	in := tx.Inputs[inputIndex]
	pre.Write(in.PrevTxID[:])
	pre.Write(uint32LE(in.PrevVout))
	pre.Write(encodeVarInt(uint64(len(scriptCode))))
	pre.Write(scriptCode)
	pre.Write(int64LE(amountSat))
	pre.Write(uint32LE(in.Sequence))
	pre.Write(hashOutputs(tx))
	pre.Write(uint32LE(tx.LockTime))
	pre.Write(uint32LE(uint32(hashType)))
	return doubleSHA256(pre.Bytes())
}

func hashPrevouts(tx *MsgTx) []byte {
	var b bytes.Buffer
	for _, in := range tx.Inputs {
		b.Write(in.PrevTxID[:])
		b.Write(uint32LE(in.PrevVout))
	}
	h := doubleSHA256(b.Bytes())
	return h[:]
}

func hashSequence(tx *MsgTx) []byte {
	var b bytes.Buffer
	for _, in := range tx.Inputs {
		b.Write(uint32LE(in.Sequence))
	}
	h := doubleSHA256(b.Bytes())
	return h[:]
}

func hashOutputs(tx *MsgTx) []byte {
	var b bytes.Buffer
	for _, out := range tx.Outputs {
		b.Write(out.serialize())
	}
	h := doubleSHA256(b.Bytes())
	return h[:]
}

func (tx *MsgTx) serialize(withWitness bool) []byte {
	var b bytes.Buffer
	b.Write(int32LE(tx.Version))
	hasWitness := withWitness && tx.hasWitness()
	if hasWitness {
		b.WriteByte(0x00)
		b.WriteByte(0x01)
	}
	b.Write(encodeVarInt(uint64(len(tx.Inputs))))
	for _, in := range tx.Inputs {
		b.Write(in.serialize())
	}
	b.Write(encodeVarInt(uint64(len(tx.Outputs))))
	for _, out := range tx.Outputs {
		b.Write(out.serialize())
	}
	if hasWitness {
		for _, in := range tx.Inputs {
			b.Write(encodeVarInt(uint64(len(in.Witness))))
			for _, item := range in.Witness {
				b.Write(encodeVarInt(uint64(len(item))))
				b.Write(item)
			}
		}
	}
	b.Write(uint32LE(tx.LockTime))
	return b.Bytes()
}

func (tx *MsgTx) copy() *MsgTx {
	out := &MsgTx{Version: tx.Version, LockTime: tx.LockTime}
	for _, in := range tx.Inputs {
		next := in
		next.ScriptSig = append([]byte(nil), in.ScriptSig...)
		next.Witness = cloneStack(in.Witness)
		out.Inputs = append(out.Inputs, next)
	}
	for _, txOut := range tx.Outputs {
		out.Outputs = append(out.Outputs, TxOut{ValueSat: txOut.ValueSat, ScriptPubKey: append([]byte(nil), txOut.ScriptPubKey...)})
	}
	return out
}

func (tx *MsgTx) hasWitness() bool {
	for _, in := range tx.Inputs {
		if len(in.Witness) > 0 {
			return true
		}
	}
	return false
}

func (in TxIn) serialize() []byte {
	var b bytes.Buffer
	b.Write(in.PrevTxID[:])
	b.Write(uint32LE(in.PrevVout))
	b.Write(encodeVarInt(uint64(len(in.ScriptSig))))
	b.Write(in.ScriptSig)
	b.Write(uint32LE(in.Sequence))
	return b.Bytes()
}

func (out TxOut) serialize() []byte {
	var b bytes.Buffer
	b.Write(int64LE(out.ValueSat))
	b.Write(encodeVarInt(uint64(len(out.ScriptPubKey))))
	b.Write(out.ScriptPubKey)
	return b.Bytes()
}

func (req *SignRequest) inputOutputSums() (int64, int64) {
	inputSum := int64(0)
	outputSum := int64(0)
	for _, in := range req.Inputs {
		inputSum += in.AmountSat
	}
	for _, out := range req.Outputs {
		outputSum += out.AmountSat
	}
	return inputSum, outputSum
}

func scriptForAddress(address, expectedHRP string) ([]byte, error) {
	hrp, version, program, err := DecodeSegwitAddress(address)
	if err != nil {
		return nil, err
	}
	if hrp != expectedHRP {
		return nil, fmt.Errorf("network hrp mismatch: got %s want %s", hrp, expectedHRP)
	}
	if version != 0 || len(program) != 20 {
		return nil, fmt.Errorf("only native P2WPKH is supported, version=%d program_len=%d", version, len(program))
	}
	return p2wpkhScript(program)
}

func extractP2WPKHPubKeyHash(script []byte) []byte {
	if len(script) == 22 && script[0] == 0x00 && script[1] == 0x14 {
		return script[2:]
	}
	return nil
}

func estimateP2WPKHVSize(inputs int, outputs int) int64 {
	return int64(10 + inputs*68 + outputs*31)
}

func virtualSize(baseSize, totalSize int) int {
	weight := baseSize*3 + totalSize
	return int(math.Ceil(float64(weight) / 4))
}

func compactSigToDER(sig []byte) ([]byte, error) {
	if len(sig) != 65 {
		return nil, fmt.Errorf("compact signature must be 65 bytes, got %d", len(sig))
	}
	r := trimDERInt(sig[:32])
	s := trimDERInt(sig[32:64])
	out := []byte{0x30, byte(4 + len(r) + len(s)), 0x02, byte(len(r))}
	out = append(out, r...)
	out = append(out, 0x02, byte(len(s)))
	out = append(out, s...)
	return out, nil
}

func trimDERInt(v []byte) []byte {
	i := 0
	for i < len(v)-1 && v[i] == 0 {
		i++
	}
	out := append([]byte(nil), v[i:]...)
	if out[0]&0x80 != 0 {
		out = append([]byte{0x00}, out...)
	}
	return out
}

func verifyDERSignature(der []byte, sighash []byte, compressedPub []byte) bool {
	r, s, err := parseDERSignature(der)
	if err != nil {
		return false
	}
	pub, err := crypto.DecompressPubkey(compressedPub)
	if err != nil {
		return false
	}
	return ecdsa.Verify(pub, sighash, new(big.Int).SetBytes(r), new(big.Int).SetBytes(s))
}

func parseDERSignature(der []byte) (r, s []byte, err error) {
	if len(der) < 8 || der[0] != 0x30 || int(der[1]) != len(der)-2 {
		return nil, nil, errors.New("invalid DER sequence")
	}
	pos := 2
	if der[pos] != 0x02 {
		return nil, nil, errors.New("invalid DER r tag")
	}
	pos++
	rLen := int(der[pos])
	pos++
	if pos+rLen >= len(der) {
		return nil, nil, errors.New("invalid DER r length")
	}
	r = der[pos : pos+rLen]
	pos += rLen
	if der[pos] != 0x02 {
		return nil, nil, errors.New("invalid DER s tag")
	}
	pos++
	sLen := int(der[pos])
	pos++
	if pos+sLen != len(der) {
		return nil, nil, errors.New("invalid DER s length")
	}
	s = der[pos : pos+sLen]
	return r, s, nil
}

func txIDStringToLittleEndian(txid string) ([32]byte, error) {
	var out [32]byte
	raw, err := hex.DecodeString(txid)
	if err != nil {
		return out, fmt.Errorf("invalid txid %q: %w", txid, err)
	}
	if len(raw) != 32 {
		return out, fmt.Errorf("txid must be 32 bytes, got %d", len(raw))
	}
	for i := range raw {
		out[i] = raw[31-i]
	}
	return out, nil
}

func displayHash(in [32]byte) string {
	out := make([]byte, 32)
	for i := range in {
		out[i] = in[31-i]
	}
	return hex.EncodeToString(out)
}

func doubleSHA256(data []byte) [32]byte {
	first := sha256.Sum256(data)
	return sha256.Sum256(first[:])
}

func int32LE(v int32) []byte {
	return uint32LE(uint32(v))
}

func uint32LE(v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return b[:]
}

func int64LE(v int64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(v))
	return b[:]
}

func encodeVarInt(v uint64) []byte {
	switch {
	case v < 0xfd:
		return []byte{byte(v)}
	case v <= 0xffff:
		var b [3]byte
		b[0] = 0xfd
		binary.LittleEndian.PutUint16(b[1:], uint16(v))
		return b[:]
	case v <= 0xffffffff:
		var b [5]byte
		b[0] = 0xfe
		binary.LittleEndian.PutUint32(b[1:], uint32(v))
		return b[:]
	default:
		var b [9]byte
		b[0] = 0xff
		binary.LittleEndian.PutUint64(b[1:], v)
		return b[:]
	}
}

func cloneStack(in [][]byte) [][]byte {
	if len(in) == 0 {
		return nil
	}
	out := make([][]byte, len(in))
	for i := range in {
		out[i] = append([]byte(nil), in[i]...)
	}
	return out
}

func parseSignedOutputs(tx *MsgTx, hrp string) []SignedOutput {
	out := make([]SignedOutput, 0, len(tx.Outputs))
	for i, txOut := range tx.Outputs {
		out = append(out, SignedOutput{
			Index:           uint32(i),
			AmountSat:       txOut.ValueSat,
			ScriptPubKeyHex: hex.EncodeToString(txOut.ScriptPubKey),
			Type:            scriptType(txOut.ScriptPubKey),
			Address:         scriptAddress(txOut.ScriptPubKey, hrp),
		})
	}
	return out
}

func scriptType(script []byte) string {
	switch {
	case len(script) == 22 && script[0] == 0x00 && script[1] == 0x14:
		return "p2wpkh"
	case len(script) == 34 && script[0] == 0x00 && script[1] == 0x20:
		return "p2wsh"
	case len(script) == 25 && script[0] == 0x76 && script[1] == 0xa9 && script[2] == 0x14 && script[23] == 0x88 && script[24] == 0xac:
		return "p2pkh"
	case len(script) == 23 && script[0] == 0xa9 && script[1] == 0x14 && script[22] == 0x87:
		return "p2sh"
	case len(script) >= 2 && script[0] == 0x6a:
		return "op_return"
	case len(script) == 34 && script[0] == 0x51 && script[1] == 0x20:
		return "p2tr"
	default:
		return "unknown"
	}
}

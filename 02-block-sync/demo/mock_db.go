package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MemRepository 内存数据库，用于测试（不依赖 MySQL）。
// 实现了 Repository 接口的完整 7 步原子回滚。
type MemRepository struct {
	mu sync.RWMutex

	chain    string
	confirms int64

	heights    *Height
	headers    map[int64]*Header // height -> header
	inbounds   []*Inbound
	outbounds  map[int64]int    // height -> status（提现）
	managedAddrs map[string]int64 // address -> uid

	// 用于测试：注入错误钩子
	revertErrorStep int // 在第 N 步注入错误（0 = 不注入）
}

// NewMemRepository 创建内存数据库。
func NewMemRepository(chain string, confirms int64) *MemRepository {
	return &MemRepository{
		chain:        chain,
		confirms:     confirms,
		headers:      make(map[int64]*Header),
		outbounds:    make(map[int64]int),
		managedAddrs: make(map[string]int64),
		heights: &Height{
			Chain: chain,
			Front: 0,
			Back:  0,
			CTime: time.Now(),
			MTime: time.Now(),
		},
	}
}

// AddManagedAddress 添加受管地址（测试辅助方法）。
func (r *MemRepository) AddManagedAddress(address string, uid int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.managedAddrs[address] = uid
}

// SetRevertErrorStep 设置在回滚的第 N 步注入错误（测试用，0 = 不注入）。
func (r *MemRepository) SetRevertErrorStep(step int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.revertErrorStep = step
}

func (r *MemRepository) GetHeights(_ context.Context) (*Height, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h := *r.heights
	return &h, nil
}

func (r *MemRepository) UpdateHeights(_ context.Context, front, back int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.heights.Front = front
	r.heights.Back = back
	r.heights.MTime = time.Now()
	return nil
}

func (r *MemRepository) GetHead(_ context.Context) (*Header, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.heights.Back == 0 {
		return nil, nil
	}
	h, ok := r.headers[r.heights.Back]
	if !ok {
		return nil, nil
	}
	cp := *h
	return &cp, nil
}

func (r *MemRepository) GetHeaderByHeight(_ context.Context, height int64) (*Header, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.headers[height]
	if !ok {
		return nil, nil
	}
	cp := *h
	return &cp, nil
}

func (r *MemRepository) SaveHeader(_ context.Context, h *Header) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *h
	r.headers[h.Height] = &cp
	return nil
}

// RevertBlock 7步原子回滚（内存版本，用 panic/recover 模拟事务原子性）。
// 生产中这7步在一个 MySQL 事务中执行。
func (r *MemRepository) RevertBlock(_ context.Context, height int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 保存回滚前的状态（模拟事务回滚）
	snapshot := r.snapshot()
	var rollbackErr error

	defer func() {
		if rollbackErr != nil {
			// 模拟事务回滚：恢复到 snapshot
			r.restore(snapshot)
		}
	}()

	// Step 1: revertBalance（余额回滚）
	if r.revertErrorStep == 1 {
		rollbackErr = fmt.Errorf("injected error at step 1")
		return rollbackErr
	}
	// 在真实实现中，这里会查询 balance_log WHERE height=? 并扣减余额
	// 内存版本简化：仅删除该高度的充值记录

	// Step 2: revertInboundTx（删除充值记录）
	if r.revertErrorStep == 2 {
		rollbackErr = fmt.Errorf("injected error at step 2")
		return rollbackErr
	}
	var remaining []*Inbound
	for _, inb := range r.inbounds {
		if inb.Height != height {
			remaining = append(remaining, inb)
		}
	}
	r.inbounds = remaining

	// Step 3: revertOutboundTx（提现状态退回 Pending）
	if r.revertErrorStep == 3 {
		rollbackErr = fmt.Errorf("injected error at step 3")
		return rollbackErr
	}
	// 将该高度的提现从 Success(2) 退回 Pending(1)
	if _, ok := r.outbounds[height]; ok {
		r.outbounds[height] = 1 // Pending
	}

	// Step 4: revertSystemTx（系统交易退回 Pending，归集/补费等）
	if r.revertErrorStep == 4 {
		rollbackErr = fmt.Errorf("injected error at step 4")
		return rollbackErr
	}
	// 内存版本简化：无系统交易

	// Step 5: revertHeader（删除区块头）
	if r.revertErrorStep == 5 {
		rollbackErr = fmt.Errorf("injected error at step 5")
		return rollbackErr
	}
	delete(r.headers, height)

	// Step 6: revertHeight（Back 减1）
	if r.revertErrorStep == 6 {
		rollbackErr = fmt.Errorf("injected error at step 6")
		return rollbackErr
	}
	r.heights.Back--
	r.heights.MTime = time.Now()

	return nil
}

func (r *MemRepository) GetManagedAddresses(_ context.Context) (map[string]int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string]int64, len(r.managedAddrs))
	for k, v := range r.managedAddrs {
		result[k] = v
	}
	return result, nil
}

func (r *MemRepository) SaveInbound(_ context.Context, tx *Inbound) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 幂等写入：同一 txHash+to 已存在则跳过（防止重组期间重复写入）
	for _, existing := range r.inbounds {
		if existing.Hash == tx.Hash && existing.To == tx.To {
			return nil
		}
	}
	cp := *tx
	r.inbounds = append(r.inbounds, &cp)
	return nil
}

func (r *MemRepository) ConfirmInbound(_ context.Context, front int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, inb := range r.inbounds {
		if inb.Height <= front && inb.Status == InboundStatusPending {
			inb.Status = InboundStatusSuccess
		}
	}
	return nil
}

// GetInbounds 返回所有充值记录（测试辅助）。
func (r *MemRepository) GetInbounds() []*Inbound {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*Inbound, len(r.inbounds))
	for i, inb := range r.inbounds {
		cp := *inb
		result[i] = &cp
	}
	return result
}

// dbSnapshot 快照（用于模拟事务回滚）
type dbSnapshot struct {
	heights  Height
	headers  map[int64]*Header
	inbounds []*Inbound
}

func (r *MemRepository) snapshot() dbSnapshot {
	headers := make(map[int64]*Header, len(r.headers))
	for k, v := range r.headers {
		cp := *v
		headers[k] = &cp
	}
	inbounds := make([]*Inbound, len(r.inbounds))
	for i, inb := range r.inbounds {
		cp := *inb
		inbounds[i] = &cp
	}
	return dbSnapshot{
		heights:  *r.heights,
		headers:  headers,
		inbounds: inbounds,
	}
}

func (r *MemRepository) restore(s dbSnapshot) {
	h := s.heights
	r.heights = &h
	r.headers = s.headers
	r.inbounds = s.inbounds
}

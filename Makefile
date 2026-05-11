.PHONY: test demo simulate run-keyman lint build tidy \
        demo-01 demo-02 demo-03 demo-04 demo-05 demo-06 demo-07

# ─────────────────────────────────────────────
# 测试
# ─────────────────────────────────────────────

test:
	go test ./... -v -race -timeout 60s

test-short:
	go test ./... -timeout 30s

# ─────────────────────────────────────────────
# 单模块 Demo
# ─────────────────────────────────────────────

demo-01:
	@echo "=== 01 Key Management Demo ==="
	go run ./01-key-management/demo/

demo-02:
	@echo "=== 02 Block Sync + Reorg Demo ==="
	go run ./02-block-sync/demo/

demo-03:
	@echo "=== 03 Deposit 7-Layer Filter Demo ==="
	go run ./03-deposit/demo/

demo-04:
	@echo "=== 04 Withdrawal Demo ==="
	go run ./04-withdrawal/demo/

demo-05:
	@echo "=== 05 Sweep Pipeline Demo ==="
	go run ./05-sweep/demo/

demo-06:
	@echo "=== 06 Reconciliation Demo ==="
	go run ./06-reconciliation/demo/

demo-07:
	@echo "=== 07 MPC Key Management Demo ==="
	go run ./07-mpc-key-management/demo/

# 所有 demo 顺序运行
demo: demo-01 demo-02 demo-03 demo-04 demo-05 demo-06 demo-07

# ─────────────────────────────────────────────
# 端到端模拟（串联所有场景）
# ─────────────────────────────────────────────

simulate:
	go run ./cmd/simulate/

# ─────────────────────────────────────────────
# 密钥管理服务（传统 walletmanager 模式）
# ─────────────────────────────────────────────

run-keyman:
	go run ./cmd/keyman/

# ─────────────────────────────────────────────
# 构建 & 工具
# ─────────────────────────────────────────────

build:
	go build ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

# AGENTS.md

本仓库是一个 Go 实现的交易所托管钱包教学/演示项目。后续协作请默认使用中文沟通。
回答问题和评审方案时，请默认站在资深钱包开发工程师的角度，优先关注密钥安全、链上资产安全、金额精度、链差异、异常回滚、风控对账和生产可运维性。

## 项目概览

- 模块路径：`github.com/yys9517/exchange-wallet`
- Go 版本：`go.mod` 声明 `go 1.22`
- 主线分支：当前为 `main`
- 代码结构按钱包核心流程拆成章节，每个章节通常包含 `README.md`、`goals.md`、`notes.md` 和可运行的 `demo/` 包。

主要目录：

- `00-architecture/`：系统架构设计文档。
- `01-key-management/`：BIP32、Scrypt、X25519、内存清除相关 demo。
- `02-block-sync/`：区块同步、重组检测、回滚逻辑。
- `03-deposit/`：充值与假充值七层防护。
- `04-withdrawal/`：提现、Nonce 管理、交易构建与签名验证。
- `05-sweep/`：归集流水线。
- `06-reconciliation/`：对账与告警去重。
- `07-mpc-key-management/`：Shamir/MPC 基础 demo。
- `cmd/`：`keyman`、`wallet`、`simulate` 等入口。
- `internal/`：可复用基础库，包括 `bigint`、`coinset`、`alarm`。
- `config/`：本地开发配置。
- `docs/`：架构、API、部署、数据库 schema 文档。

## 常用命令

- `make test-short`：运行全部测试，超时 30 秒。
- `make test`：运行全部测试，开启 `-race`，超时 60 秒。
- `make simulate`：运行端到端 7 场景模拟。
- `make demo`：顺序运行所有章节 demo。
- `make demo-01` 到 `make demo-07`：运行单个章节 demo。
- `make build`：构建全部包。
- `make tidy`：整理 Go modules。

## 当前环境注意点

初始化时尝试运行 `make test-short` 和 `make simulate`：

- 首次运行被沙箱阻止写入用户 Go 构建缓存目录。
- 放行后编译失败，原因为 Go 工具链环境混用：
  - `go version` 输出 `go1.22.0 darwin/arm64`
  - `which go` 指向 `/Users/dawanyezhi/.gvm/gos/go1.22.0/bin/go`
  - `go env GOROOT` 却是 `/Users/dawanyezhi/.gvm/gos/go1.24.0`
  - 错误表现为 `compile: version "go1.24.0" does not match go tool version "go1.22.0"`

在修复代码前，优先确认本机 GVM/Go 环境一致。建议让 `GOROOT` 与当前 `go` 二进制版本匹配，或切换到同一套 Go 版本后再运行测试。

## 开发约定

- 优先保持现有教学项目风格：模块独立、demo 可运行、测试覆盖核心行为。
- 金额相关逻辑使用 `internal/bigint` 或 `math/big`，避免 `float64`。
- 链差异优先通过 `internal/coinset` 的链配置和 feature gate 表达。
- 修改 demo 行为时，同步检查对应章节的测试和文档描述是否需要更新。
- 不要把生产不可用的 demo 简化点包装成生产能力；文档中应明确 demo 与生产系统差距。
- 提交前尽量运行相关包测试；如果受本机 Go 环境限制无法验证，要在交付说明中明确说明。

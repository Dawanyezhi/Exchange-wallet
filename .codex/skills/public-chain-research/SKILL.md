---
name: public-chain-research
description: 通用公链接入调研与交易所托管钱包方案设计。用于调研新公链、补全链接入文档、评审充值/提现/归集/对账方案、判断 Account/UTXO/Tag/Memo 链差异、设计 RPC 调用、数据模型、签名流程、费用模型、重组回滚和生产安全检查清单。
---

# 公链接入调研

## 使用方式

当用户要求调研一条公链、生成链接入方案、评审钱包支持能力或把某条链接入本项目时，使用本 skill。默认站在资深交易所钱包工程师视角，优先关注资产安全、密钥安全、金额精度、链差异、重组回滚、假充值防护、费用控制、对账和生产可运维性。

## 工作流程

1. 明确目标链、网络和支持范围：主币、Token、NFT、Memo/Tag、L2/桥、主网/测试网。
2. 优先查官方资料：官方开发者文档、RPC 文档、节点客户端文档、BIP/EIP/SLIP/CAIP 等标准；浏览器和第三方数据平台只能作为辅助。
3. 先判断链类型：Account、UTXO、Tag/Memo、多资产合约链或 L2/桥接链。
4. 按调研模板补齐链特性、地址体系、签名、交易构建、RPC、节点部署、费用模型、重组/finality 和历史风险。
5. 从钱包业务闭环出发设计数据模型：地址、充值、提现、系统交易、余额、区块、高度、UTXO/Vin/Vout、Memo/Tag、交易记录、raw tx、审计和对账。
6. 给出可落地结论：是否适合交易所接入、最小上线能力、不建议首期支持的能力、生产风险点、对当前项目的改造建议。

## 参考文档选择

- 写完整公链调研报告：读取 `references/chain-research-template.md`。
- 判断 Account/UTXO/Tag/L2 等链类型方案：读取 `references/chain-type-patterns.md`。
- 调研 RPC、节点或数据平台能力：读取 `references/rpc-research-template.md`。
- 设计数据库表、状态机和回滚索引：读取 `references/wallet-data-model-template.md`。
- 做方案评审或安全检查：读取 `references/security-review-checklist.md`。
- 输出需要引用资料或固定交付格式：读取 `references/source-and-output-rules.md`。

## 输出要求

输出必须明确区分“官方资料确认的事实”和“基于钱包工程经验的建议”。如果信息可能随时间变化，例如 RPC 版本、节点参数、费率市场、确认数建议、Token 协议状态、浏览器能力，必须联网或查最新官方资料确认。

不要把 demo 能力包装成生产能力。若用户要落地到本仓库，优先沿用 `internal/coinset` 的链类型和 FeatureGate 思路，金额使用整数或 `math/big`/`internal/bigint`，并指出需要新增或调整的表结构、测试和文档。

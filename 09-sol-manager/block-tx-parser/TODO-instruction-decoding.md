# Solana Raw Instruction Decoding TODO

本文记录 `prod-new.json` 这类主网 `getBlock` 返回中 raw instruction 的后续解析方案。当前 demo 首期只保证扫块、余额差额、Token 充值候选能被解析；不完整反解所有 Token、Jupiter、DEX/AMM 指令。

## 当前主网结构

`prod-new.json` 使用官方 JSON 交易结构，不是 `jsonParsed`：

- `transaction.message.accountKeys` 是 base58 字符串数组。
- `transaction.message.instructions[].programIdIndex` 指向 `accountKeys` 中的 program id。
- `transaction.message.instructions[].accounts` 是账户下标数组，也指向 resolved account keys。
- `transaction.message.instructions[].data` 是 base58 编码的程序输入。
- v0 transaction 需要把 `meta.loadedAddresses.writable` 和 `meta.loadedAddresses.readonly` 追加到静态 `accountKeys` 后，形成 resolved account keys。
- `meta.innerInstructions` 里的 instruction 结构与外层 raw instruction 一致。

通用解析步骤：

1. 解包完整 JSON-RPC 响应的 `result`。
2. 解析静态 `accountKeys`。
3. 对 v0 交易追加 `loadedAddresses.writable` 和 `loadedAddresses.readonly`。
4. 用 `programIdIndex` 还原 `program_id`。
5. 用 `accounts` 下标还原 instruction 使用的账户地址。
6. base58 解码 `data`。
7. 根据 `program_id` 选择对应 decoder。
8. 外层 instruction 用 `instruction_index` 标识，inner instruction 用 `instruction_index + inner_index` 标识。

## 已实现边界

- 支持完整 JSON-RPC `getBlock` 响应和已解包 block。
- 支持 `accountKeys` 为字符串数组或 `jsonParsed` 对象数组。
- 支持 v0 `loadedAddresses` 拼接。
- 支持 System Program raw `transfer` 的最小解码。
- SPL Token 充值以 `preTokenBalances/postTokenBalances` 的 base units 差额为准。
- 失败交易 `meta.err != null` 不生成可入账 Token delta。

## SPL Token 充值入账边界

生产入账不依赖完整反解所有 Token instruction data。推荐顺序：

1. `meta.err == null`，失败交易不入账。
2. 遍历 `postTokenBalances`，与 `preTokenBalances` 按 `accountIndex + mint + programId` 配对，计算余额增量。
3. 对 `delta > 0` 的记录，先校验 `mint` 命中支持的 Token 白名单。
4. 校验 `programId` 与白名单中的 `token_program_id` 一致，区分 SPL Token 和 Token-2022。
5. 校验 `decimals` 与白名单一致。
6. 根据 `accountIndex` 从 resolved account keys 还原 token account 地址。
7. 校验 token account 命中我方 `wallet_sol_token_accounts`。
8. 校验 token account 的 `owner_address/uid` 归属正确。
9. 使用 base units 大整数生成充值金额。

## 后续 decoder 清单

### System Program

用途：

- SOL 主币充值、提现、租金相关账户创建。

后续实现：

- 已实现 `transfer`：instruction id `2`，layout 为 `uint32 little-endian instruction_id + uint64 little-endian lamports`。
- 待补充 `createAccount`、`assign`、`transferWithSeed` 等 System 指令。
- 对生产充值来说，SOL 入账仍应以 `preBalances/postBalances` 和我方地址命中为准，System transfer 解码作为辅助审计字段。

资料来源：

- Solana System Program SDK 源码。
- `solana-program` / `solana-sdk` 中 `system_instruction` layout。

### SPL Token Program

用途：

- SPL Token 转账、账户关闭、授权、冻结、解冻。

后续实现：

- 解码 `TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA` 的 instruction enum。
- 优先覆盖 `Transfer`、`TransferChecked`、`InitializeAccount`、`CloseAccount`、`Approve`、`SetAuthority`。
- 对 `TransferChecked` 反解 source、mint、destination、authority、amount、decimals。
- 生产入账仍以 token balance delta 为准；Token instruction 解码用于审计、异常排查和提现签后反解析。

资料来源：

- SPL Token Program 官方文档。
- `spl-token` crate 的 instruction enum 和 pack/unpack layout。

### Token-2022 Program

用途：

- 带 Transfer Fee、Transfer Hook、Memo Transfer、Default Account State 等扩展的 Token。

后续实现：

- 以 Token-2022 program id 单独建 decoder。
- 解码基础 transfer 指令后，还必须按 mint 白名单检查扩展配置。
- 对 Transfer Fee 类 mint，入账金额应以 post-pre 实际到账 delta 为准，而不是 instruction amount。

资料来源：

- Token-2022 官方文档。
- `spl-token-2022` crate 的 extension 和 instruction layout。

### Associated Token Account Program

用途：

- 创建 ATA，识别目标 owner/mint/token_program。

后续实现：

- 解码 `createAssociatedTokenAccount`、`createIdempotent`、`recoverNested`。
- 从账户列表还原 payer、associated token account、wallet owner、mint、system program、token program。
- 在提现签后反解析中校验创建目标只能是提现目标 owner 的 ATA。

资料来源：

- Associated Token Account Program 官方文档。
- `spl-associated-token-account` crate。

### Compute Budget Program

用途：

- priority fee、compute unit limit/price。

后续实现：

- 解码 `setComputeUnitLimit` 和 `setComputeUnitPrice`。
- 广播前复算 priority fee，并校验不超过业务风控阈值。

资料来源：

- Solana Compute Budget Program SDK 源码。

### Memo Program

用途：

- 共享地址充值归属、备注审计。

后续实现：

- 将 instruction data 解码为 UTF-8 memo。
- 共享地址模式下 memo 必须参与用户归属和唯一键。
- 独立地址模式下 memo 只作为辅助审计字段。

资料来源：

- SPL Memo Program 官方文档。

### Jupiter

用途：

- Swap 聚合器交易。主网样本中 Jupiter 指令较多，但交易所充值不应依赖完整 Jupiter 指令反解。

后续实现：

- 先按 program id 白名单识别 Jupiter 指令，并记录 raw program id、账户列表、data。
- 如需业务审计 swap 路径，使用 Jupiter 官方 SDK/IDL 或公开 instruction layout 解码 route/swap 参数。
- 对充值判断仍以 token balance delta、mint 白名单、token account 归属为准。

资料来源：

- Jupiter 官方文档和 SDK。
- Jupiter program id 白名单。
- 若使用 Anchor，按 discriminator + IDL 解码。

### DEX/AMM Programs

用途：

- Raydium、Orca、Phoenix、Meteora 等 DEX/AMM 产生的 token account 余额变化。

后续实现：

- 建立 program id 白名单和 decoder registry。
- Anchor 程序按 8 字节 discriminator + IDL 解码。
- 非 Anchor 程序按官方 SDK 或 crate layout 解码。
- 先记录 unknown program raw instruction，不阻断充值余额差额解析。

资料来源：

- 各 DEX 官方 SDK/IDL。
- Anchor IDL。
- 链上 program id 白名单和版本管理。

## 生产注意事项

- 不要用 symbol 判断资产，必须使用 `mint + token_program_id`。
- 不支持 mint 即使打到我方 token account，也不能自动入账。
- `uiAmount`/`uiAmountString` 只能展示，入账必须使用 `uiTokenAmount.amount` 的 base units 字符串。
- `meta.err != null` 的交易不能入账。
- public RPC 可能缺历史或版本能力，生产需要自建 RPC、可靠供应商或索引器补偿。
- unknown instruction 不等于异常充值；充值判断以 balance delta 和白名单/归属校验为准。

-- exchange-wallet 完整数据库建表语句
-- 命名规范：wallet_ 前缀，snake_case
-- 数据库：MySQL 8.0+
-- 编码：utf8mb4

CREATE DATABASE IF NOT EXISTS `exchange_wallet` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE `exchange_wallet`;

-- ============================================================
-- 区块同步相关表
-- ============================================================

-- 区块头表（滑动窗口）：保存 Front ~ Back 范围内的区块头
CREATE TABLE `wallet_blocks` (
    `chain`  VARCHAR(20)  NOT NULL COMMENT '链标识，如 ETH/BSC',
    `height` BIGINT       NOT NULL COMMENT '块高度',
    `hash`   VARCHAR(66)  NOT NULL COMMENT '块哈希（含0x前缀，66字符）',
    `parent` VARCHAR(66)  NOT NULL COMMENT '父块哈希（重组检测核心字段）',
    `btime`  DATETIME     NOT NULL COMMENT '出块时间',
    PRIMARY KEY (`chain`, `height`),
    KEY `idx_chain_hash` (`chain`, `hash`) COMMENT '按哈希查找块（重组时使用）'
) ENGINE=InnoDB COMMENT='区块头滑动窗口，用于 parentHash 重组检测';

-- 高度指针表（Front/Back 滑动窗口）
CREATE TABLE `wallet_heights` (
    `chain`  VARCHAR(20)  NOT NULL PRIMARY KEY COMMENT '链标识',
    `front`  BIGINT       NOT NULL DEFAULT 0 COMMENT '安全高度（= back - 确认数），此高度以下可上账',
    `back`   BIGINT       NOT NULL DEFAULT 0 COMMENT '已扫描的最新块高度',
    `ctime`  DATETIME     NOT NULL COMMENT '创建时间',
    `mtime`  DATETIME     NOT NULL COMMENT '最后更新时间'
) ENGINE=InnoDB COMMENT='Front/Back 滑动窗口高度指针';

-- ============================================================
-- 账户与余额相关表
-- ============================================================

-- 账户余额快照表
CREATE TABLE `wallet_accounts` (
    `chain`   VARCHAR(20)  NOT NULL COMMENT '链标识',
    `address` VARCHAR(42)  NOT NULL COMMENT '地址（EVM: 0x开头42字符）',
    `uid`     BIGINT       NOT NULL COMMENT '用户ID（0=系统账户）',
    `kind`    TINYINT      NOT NULL DEFAULT 0 COMMENT '0=User 1=Hot 2=Cold',
    `symbol`  VARCHAR(20)  NOT NULL DEFAULT 'ETH' COMMENT '代币符号',
    `pubkey`  BLOB         COMMENT '公钥（用于验签）',
    `balance` VARCHAR(40)  NOT NULL DEFAULT '0' COMMENT '当前余额快照（big.Int字符串，wei）',
    `ctime`   DATETIME     NOT NULL COMMENT '创建时间',
    `mtime`   DATETIME     NOT NULL COMMENT '最后更新时间',
    PRIMARY KEY (`chain`, `address`, `symbol`),
    KEY `idx_uid_chain` (`uid`, `chain`) COMMENT '按用户查询所有链的余额'
) ENGINE=InnoDB COMMENT='账户余额快照（由 balance_log 聚合而来）';

-- 余额变更历史表（支持按块高度回滚）
CREATE TABLE `wallet_balance_log` (
    `id`     BIGINT       AUTO_INCREMENT PRIMARY KEY,
    `chain`  VARCHAR(20)  NOT NULL COMMENT '链标识',
    `symbol` VARCHAR(20)  NOT NULL COMMENT '代币符号',
    `uid`    BIGINT       NOT NULL COMMENT '用户ID',
    `height` BIGINT       NOT NULL COMMENT '产生变更的块高度（回滚时按高度删除）',
    `delta`  VARCHAR(40)  NOT NULL COMMENT '变更量（正=充值增加，负=提现减少，big.Int字符串）',
    `source` VARCHAR(20)  NOT NULL COMMENT '来源：inbound/outbound/sweep',
    `ref_id` BIGINT       NOT NULL DEFAULT 0 COMMENT '关联记录ID（inbound.id/outbound.id）',
    `ctime`  DATETIME     NOT NULL COMMENT '记录时间',
    KEY `idx_chain_height` (`chain`, `height`) COMMENT '按高度回滚时的索引',
    KEY `idx_uid_chain` (`uid`, `chain`) COMMENT '查询用户变更历史'
) ENGINE=InnoDB COMMENT='余额变更历史，支持按块高度精确回滚';

-- ============================================================
-- 充值（Inbound）相关表
-- ============================================================

-- 充值记录表
CREATE TABLE `wallet_inbound` (
    `id`     BIGINT       AUTO_INCREMENT PRIMARY KEY,
    `chain`  VARCHAR(20)  NOT NULL COMMENT '链标识',
    `symbol` VARCHAR(20)  NOT NULL COMMENT '代币符号，如 ETH/USDT',
    `height` BIGINT       NOT NULL COMMENT '确认块高度（0表示未确认）',
    `hash`   VARCHAR(66)  NOT NULL COMMENT '交易哈希',
    `from`   VARCHAR(42)  NOT NULL COMMENT '发送方地址',
    `to`     VARCHAR(42)  NOT NULL COMMENT '接收方地址（用户充值地址）',
    `value`  VARCHAR(40)  NOT NULL COMMENT '充值金额（big.Int字符串，链上最小单位）',
    `fee`    VARCHAR(40)  NOT NULL DEFAULT '0' COMMENT '链上手续费',
    `status` TINYINT      NOT NULL DEFAULT 0 COMMENT '0=Pending 1=Success 4=Revert',
    `uid`    BIGINT       NOT NULL COMMENT '对应的用户ID',
    `btime`  DATETIME     NOT NULL COMMENT '出块时间',
    `ctime`  DATETIME     NOT NULL COMMENT '记录创建时间',
    `mtime`  DATETIME     NOT NULL COMMENT '最后更新时间',
    UNIQUE KEY `uk_hash_to` (`hash`, `to`) COMMENT '防止同一交易对同一地址重复计入',
    KEY `idx_uid_chain` (`uid`, `chain`) COMMENT '查询用户充值记录',
    KEY `idx_chain_height` (`chain`, `height`) COMMENT '按高度查询（重组回滚使用）'
) ENGINE=InnoDB COMMENT='充值记录（经过7层过滤后的有效充值）';

-- ============================================================
-- 提现（Outbound）相关表
-- ============================================================

-- 提现记录表
CREATE TABLE `wallet_outbound` (
    `id`     BIGINT       AUTO_INCREMENT PRIMARY KEY,
    `chain`  VARCHAR(20)  NOT NULL COMMENT '链标识',
    `symbol` VARCHAR(20)  NOT NULL COMMENT '代币符号',
    `height` BIGINT       NOT NULL DEFAULT 0 COMMENT '确认块高度（0表示未上链）',
    `hash`   VARCHAR(66)  NOT NULL DEFAULT '' COMMENT '交易哈希',
    `from`   VARCHAR(42)  NOT NULL COMMENT '热钱包地址',
    `to`     VARCHAR(42)  NOT NULL COMMENT '用户提现目标地址',
    `value`  VARCHAR(40)  NOT NULL COMMENT '提现金额（big.Int字符串）',
    `fee`    VARCHAR(40)  NOT NULL DEFAULT '0' COMMENT '实际消耗的手续费',
    `status` TINYINT      NOT NULL DEFAULT 0 COMMENT '0=Ready 1=Pending 2=Success 3=Failure 4=Revert',
    `nonce`  BIGINT       NOT NULL DEFAULT 0 COMMENT '交易Nonce（用于卡Nonce排查）',
    `qid`    BIGINT       NOT NULL COMMENT '交易所提现单ID（幂等键）',
    `uid`    BIGINT       NOT NULL COMMENT '用户ID',
    `rawtx`  BLOB         COMMENT '原始交易（hex），用于重发',
    `btime`  DATETIME     COMMENT '上链时间',
    `ctime`  DATETIME     NOT NULL COMMENT '创建时间',
    `mtime`  DATETIME     NOT NULL COMMENT '最后更新时间',
    UNIQUE KEY `uk_qid` (`chain`, `qid`) COMMENT '提现单幂等性保证',
    KEY `idx_chain_height` (`chain`, `height`) COMMENT '按高度查询（重组回滚使用）',
    KEY `idx_status` (`chain`, `status`) COMMENT '查询待处理的提现'
) ENGINE=InnoDB COMMENT='提现记录';

-- ============================================================
-- 归集（Sweep）相关表
-- ============================================================

-- 系统交易表（归集/补费）
CREATE TABLE `wallet_system_txs` (
    `id`     BIGINT       AUTO_INCREMENT PRIMARY KEY,
    `chain`  VARCHAR(20)  NOT NULL COMMENT '链标识',
    `kind`   VARCHAR(20)  NOT NULL COMMENT 'sweep=归集 fee=补费',
    `height` BIGINT       NOT NULL DEFAULT 0 COMMENT '确认块高度',
    `hash`   VARCHAR(66)  NOT NULL DEFAULT '' COMMENT '交易哈希',
    `from`   VARCHAR(42)  NOT NULL COMMENT '发送方地址',
    `to`     VARCHAR(42)  NOT NULL COMMENT '接收方地址',
    `value`  VARCHAR(40)  NOT NULL COMMENT '转账金额',
    `status` TINYINT      NOT NULL DEFAULT 0 COMMENT '0=Pending 1=Success 3=Failure',
    `ctime`  DATETIME     NOT NULL COMMENT '创建时间',
    `mtime`  DATETIME     NOT NULL COMMENT '最后更新时间',
    KEY `idx_chain_height` (`chain`, `height`) COMMENT '按高度查询（重组回滚使用）'
) ENGINE=InnoDB COMMENT='系统交易（归集和补费）';

-- ============================================================
-- 密钥管理服务（keyman）相关表
-- 独立数据库：wallet_keyman，与钱包服务物理隔离
-- ============================================================

CREATE DATABASE IF NOT EXISTS `wallet_keyman` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE `wallet_keyman`;

-- 主种子加密存储表
-- 设计决策：
--   生产中主种子以 Web3 Keystore V3 格式（Scrypt+AES-128-CTR）加密后存入此表
--   同一 seed_id 只有一条有效记录（is_active=1），密钥轮换时新增记录并废弃旧记录
--   也可选择文件存储（keystore/root.json），此表提供 DB 方案作为替代
CREATE TABLE `keyman_master_seed` (
    `id`         BIGINT       AUTO_INCREMENT PRIMARY KEY,
    `seed_id`    VARCHAR(36)  NOT NULL COMMENT '种子唯一标识（UUID），用于多套种子共存',
    `version`    INT          NOT NULL DEFAULT 3 COMMENT 'Keystore 版本（3=Web3 V3）',
    `crypto`     JSON         NOT NULL COMMENT 'Web3 Keystore V3 加密字段（cipher/ciphertext/cipherparams/kdf/kdfparams/mac）',
    `is_active`  TINYINT      NOT NULL DEFAULT 1 COMMENT '1=当前使用 0=已轮换废弃',
    `comment`    VARCHAR(200) NOT NULL DEFAULT '' COMMENT '备注（如：initial seed / rotated on 2024-01-15）',
    `ctime`      DATETIME     NOT NULL COMMENT '创建时间',
    `mtime`      DATETIME     NOT NULL COMMENT '最后更新时间',
    UNIQUE KEY `uk_seed_active` (`seed_id`, `is_active`) COMMENT '同一 seed_id 只能有一条 active'
) ENGINE=InnoDB COMMENT='主种子加密存储（Scrypt+AES-128-CTR，兼容 Web3 Keystore V3）';

-- crypto 字段示例（与 passphrase.go 的 CryptoJSON 完全对应）：
-- {
--   "cipher": "aes-128-ctr",
--   "ciphertext": "d172bf7...",
--   "cipherparams": {"iv": "83dbcc0..."},
--   "kdf": "scrypt",
--   "kdfparams": {"n": 262144, "r": 8, "p": 1, "dklen": 32, "salt": "ab0c7..."},
--   "mac": "2103ac29..."
-- }

-- 地址派生记录表
-- 设计决策：
--   记录每个已派生地址对应的 BIP32 路径，签名时通过 (chain, address) 反查 account_index
--   derivation_path 存完整路径字符串，方便调试和审计
--   公钥单独存储，wallet 服务可通过 API 获取公钥而不接触私钥
CREATE TABLE `keyman_derived_addresses` (
    `id`               BIGINT       AUTO_INCREMENT PRIMARY KEY,
    `seed_id`          VARCHAR(36)  NOT NULL COMMENT '关联的主种子标识',
    `chain`            VARCHAR(20)  NOT NULL COMMENT '链标识（ETH/BSC/POLYGON）',
    `account_index`    INT UNSIGNED NOT NULL COMMENT 'BIP32 派生路径中的 account 层级索引',
    `derivation_path`  VARCHAR(100) NOT NULL COMMENT '完整派生路径，如 m/fnv1a(ETH)''/0''',
    `address`          VARCHAR(42)  NOT NULL COMMENT '派生出的地址',
    `pubkey`           VARBINARY(65) NOT NULL COMMENT '压缩公钥（33字节 secp256k1 / 32字节 Ed25519）',
    `kind`             TINYINT      NOT NULL DEFAULT 0 COMMENT '0=User 1=Hot 2=Cold',
    `status`           TINYINT      NOT NULL DEFAULT 1 COMMENT '1=Active 0=Disabled（密钥泄漏时禁用）',
    `ctime`            DATETIME     NOT NULL COMMENT '派生时间',
    UNIQUE KEY `uk_chain_address` (`chain`, `address`) COMMENT '同一链地址不能重复',
    KEY `idx_chain_index` (`chain`, `account_index`) COMMENT '按索引查询（批量派生时使用）',
    KEY `idx_seed` (`seed_id`) COMMENT '按种子查询所有派生地址'
) ENGINE=InnoDB COMMENT='BIP32 派生地址记录（存公钥，不存私钥）';

-- 派生索引计数器表
-- 设计决策：
--   每条链维护一个自增计数器，记录下一个可用的 account_index
--   避免服务重启后重复派生相同索引（导致地址冲突）或跳号（导致地址丢失）
--   使用 SELECT ... FOR UPDATE 保证并发安全
CREATE TABLE `keyman_derivation_counter` (
    `chain`      VARCHAR(20)  NOT NULL PRIMARY KEY COMMENT '链标识',
    `next_index` INT UNSIGNED NOT NULL DEFAULT 0 COMMENT '下一个可用的 account_index',
    `mtime`      DATETIME     NOT NULL COMMENT '最后更新时间'
) ENGINE=InnoDB COMMENT='派生索引计数器（防止重启后索引错乱）';

-- 签名审计日志表
-- 设计决策：
--   所有签名请求必须记录审计日志（合规要求）
--   不存储签名结果（签名本身不是秘密），只记录"谁请求签名了什么"
--   request_ip + caller_service 用于事后溯源
--   生产中此表应只增不改不删，定期归档到冷存储
CREATE TABLE `keyman_sign_audit` (
    `id`              BIGINT       AUTO_INCREMENT PRIMARY KEY,
    `chain`           VARCHAR(20)  NOT NULL COMMENT '链标识',
    `address`         VARCHAR(42)  NOT NULL COMMENT '签名地址',
    `tx_hash`         VARCHAR(66)  NOT NULL COMMENT '被签名的交易哈希',
    `caller_service`  VARCHAR(50)  NOT NULL COMMENT '请求方服务名（wallet/sweep/withdrawal）',
    `request_ip`      VARCHAR(45)  NOT NULL COMMENT '请求来源 IP',
    `result`          VARCHAR(10)  NOT NULL COMMENT 'success / denied / error',
    `deny_reason`     VARCHAR(200) NOT NULL DEFAULT '' COMMENT '拒绝原因（如频率限制、黑名单）',
    `ctime`           DATETIME     NOT NULL COMMENT '请求时间',
    KEY `idx_chain_address` (`chain`, `address`) COMMENT '按地址查询签名历史',
    KEY `idx_ctime` (`ctime`) COMMENT '按时间范围查询'
) ENGINE=InnoDB COMMENT='签名审计日志（只增不改不删，定期归档）';

-- exchange-wallet 数据库表结构（02-block-sync 模块）

-- 区块头表（滑动窗口）：保存 Front ~ Back 范围内的区块头，用于重组检测
CREATE TABLE `wallet_blocks` (
    `chain`  VARCHAR(20) NOT NULL COMMENT '链标识，如 ETH/BSC',
    `height` BIGINT      NOT NULL COMMENT '块高度',
    `hash`   VARCHAR(66) NOT NULL COMMENT '块哈希',
    `parent` VARCHAR(66) NOT NULL COMMENT '父块哈希（重组检测核心字段）',
    `btime`  DATETIME    NOT NULL COMMENT '出块时间',
    PRIMARY KEY (`chain`, `height`)
) ENGINE=InnoDB COMMENT='区块头滑动窗口，用于 parentHash 重组检测';

-- 高度指针表（Front/Back）
CREATE TABLE `wallet_heights` (
    `chain`  VARCHAR(20) NOT NULL PRIMARY KEY COMMENT '链标识',
    `front`  BIGINT      NOT NULL DEFAULT 0 COMMENT '安全高度（= back - 确认数），此高度以下可上账',
    `back`   BIGINT      NOT NULL DEFAULT 0 COMMENT '已扫描的最新块高度',
    `ctime`  DATETIME    NOT NULL COMMENT '创建时间',
    `mtime`  DATETIME    NOT NULL COMMENT '最后更新时间'
) ENGINE=InnoDB COMMENT='Front/Back 滑动窗口高度指针';

-- 充值记录表
CREATE TABLE `wallet_inbound` (
    `id`     BIGINT      AUTO_INCREMENT PRIMARY KEY,
    `chain`  VARCHAR(20) NOT NULL COMMENT '链标识',
    `symbol` VARCHAR(20) NOT NULL COMMENT '代币符号，如 ETH/USDT',
    `height` BIGINT      NOT NULL COMMENT '所在块高度',
    `hash`   VARCHAR(66) NOT NULL COMMENT '交易哈希',
    `from`   VARCHAR(42) NOT NULL COMMENT '发送方地址',
    `to`     VARCHAR(42) NOT NULL COMMENT '接收方地址（用户充值地址）',
    `value`  VARCHAR(40) NOT NULL COMMENT '金额（big.Int 字符串，链上最小单位）',
    `fee`    VARCHAR(40) NOT NULL DEFAULT '0' COMMENT '手续费',
    `status` TINYINT     NOT NULL DEFAULT 0 COMMENT '0=Pending 1=Success 4=Revert',
    `uid`    BIGINT      NOT NULL COMMENT '对应的用户 ID',
    `btime`  DATETIME    NOT NULL COMMENT '出块时间',
    `ctime`  DATETIME    NOT NULL COMMENT '记录创建时间',
    `mtime`  DATETIME    NOT NULL COMMENT '最后更新时间',
    UNIQUE KEY `uk_hash_to` (`hash`, `to`) COMMENT '防止同一交易对同一地址重复计入'
) ENGINE=InnoDB COMMENT='充值记录';

-- 提现记录表
CREATE TABLE `wallet_outbound` (
    `id`     BIGINT      AUTO_INCREMENT PRIMARY KEY,
    `chain`  VARCHAR(20) NOT NULL COMMENT '链标识',
    `symbol` VARCHAR(20) NOT NULL COMMENT '代币符号',
    `height` BIGINT      NOT NULL DEFAULT 0 COMMENT '确认块高度（0表示未上链）',
    `hash`   VARCHAR(66) NOT NULL DEFAULT '' COMMENT '交易哈希',
    `from`   VARCHAR(42) NOT NULL COMMENT '热钱包地址',
    `to`     VARCHAR(42) NOT NULL COMMENT '用户提现目标地址',
    `value`  VARCHAR(40) NOT NULL COMMENT '提现金额',
    `fee`    VARCHAR(40) NOT NULL DEFAULT '0' COMMENT '实际手续费',
    `status` TINYINT     NOT NULL DEFAULT 0 COMMENT '0=Ready 1=Pending 2=Success 3=Failure 4=Revert',
    `qid`    BIGINT      NOT NULL COMMENT '交易所提现单 ID（幂等键）',
    `uid`    BIGINT      NOT NULL COMMENT '用户 ID',
    `rawtx`  BLOB        COMMENT '原始交易（hex），用于重发',
    `btime`  DATETIME    COMMENT '上链时间',
    `ctime`  DATETIME    NOT NULL COMMENT '创建时间',
    `mtime`  DATETIME    NOT NULL COMMENT '最后更新时间'
) ENGINE=InnoDB COMMENT='提现记录';

-- 余额变更历史表（支持回滚）
CREATE TABLE `wallet_balance_log` (
    `id`     BIGINT      AUTO_INCREMENT PRIMARY KEY,
    `chain`  VARCHAR(20) NOT NULL COMMENT '链标识',
    `symbol` VARCHAR(20) NOT NULL COMMENT '代币符号',
    `uid`    BIGINT      NOT NULL COMMENT '用户 ID',
    `height` BIGINT      NOT NULL COMMENT '产生变更的块高度（回滚时按高度删除）',
    `delta`  VARCHAR(40) NOT NULL COMMENT '变更量（正=充值增加，负=提现减少）',
    `ctime`  DATETIME    NOT NULL COMMENT '记录时间',
    KEY `idx_chain_height` (`chain`, `height`) COMMENT '按高度回滚时的索引'
) ENGINE=InnoDB COMMENT='余额变更历史，支持按块高度回滚';

-- 账户余额快照表
CREATE TABLE `wallet_accounts` (
    `chain`   VARCHAR(20) NOT NULL COMMENT '链标识',
    `address` VARCHAR(42) NOT NULL COMMENT '地址',
    `uid`     BIGINT      NOT NULL COMMENT '用户 ID',
    `kind`    TINYINT     NOT NULL DEFAULT 0 COMMENT '0=User 1=Hot 2=Cold',
    `pubkey`  BLOB        COMMENT '公钥（用于验签）',
    `balance` VARCHAR(40) NOT NULL DEFAULT '0' COMMENT '当前余额快照',
    `ctime`   DATETIME    NOT NULL COMMENT '创建时间',
    `mtime`   DATETIME    NOT NULL COMMENT '最后更新时间',
    PRIMARY KEY (`chain`, `address`)
) ENGINE=InnoDB COMMENT='账户余额快照（由 balance_log 聚合而来）';

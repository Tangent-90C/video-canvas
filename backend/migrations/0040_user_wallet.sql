-- 用户 TokenPay（TokenDance）钱包：
-- 1) ai_channels.wallet_enabled 标记「钱包渠道」——这类渠道上的模型由发起任务的用户自己的 TokenDance Key 支付，
--    平台不冻结 / 不扣积分（运营者通过 TokenPay 分润结算）；
-- 2) generation_tasks.wallet 标记钱包任务：执行时用发起用户的 Key（ai_secrets 里 wallet:<user_id>），不计费；
-- 3) user_wallets 存授权元数据（Key 密文在 ai_secrets，名字 wallet:<user_id>，AES-256-GCM 与渠道 Key 同一套）。
CREATE TABLE user_wallets (
    user_id bigint PRIMARY KEY,
    provider varchar(32) NOT NULL,
    app_url varchar(512) NOT NULL DEFAULT '',
    key_masked varchar(64) NOT NULL DEFAULT '',
    created_at timestamptz,
    updated_at timestamptz
);
ALTER TABLE ai_channels ADD COLUMN wallet_enabled bool NOT NULL DEFAULT false;
ALTER TABLE generation_tasks ADD COLUMN wallet bool NOT NULL DEFAULT false;

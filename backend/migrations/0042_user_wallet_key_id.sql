-- 钱包界面展示 TokenPay 后台可见的公开 Key ID，而不是用户看不到的授权 Key 掩码。
-- 授权码交换接口只返回 Key 本身，不返回 Key ID；已连接行由调用记录里的 key_id 在首次读取时回填。
ALTER TABLE user_wallets ADD COLUMN key_id varchar(32) NOT NULL DEFAULT '';

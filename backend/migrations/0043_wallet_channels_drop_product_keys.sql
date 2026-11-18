-- TokenPay 钱包渠道的模型目录是公开接口，用户调用使用 wallet:<uid>。
-- 清除历史版本误存的产品方渠道凭证，避免后台展示或任何代码再次误用它。
DELETE FROM ai_secrets
WHERE name IN (
    SELECT 'channel:' || key FROM ai_channels WHERE wallet_enabled
    UNION
    SELECT 'channel:' || key || ':secret' FROM ai_channels WHERE wallet_enabled
);

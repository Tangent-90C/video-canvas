-- 观猹（Watcha）OAuth 登录身份：provider + subject 唯一，一个第三方账号只绑定一个本站用户。
CREATE TABLE user_oauth_identities (
    id bigserial PRIMARY KEY,
    created_at timestamptz,
    updated_at timestamptz,
    deleted_at timestamptz,
    user_id bigint NOT NULL,
    provider varchar(32) NOT NULL,
    subject varchar(128) NOT NULL
);
CREATE INDEX idx_user_oauth_identities_deleted_at ON user_oauth_identities (deleted_at);
CREATE INDEX idx_user_oauth_identities_user_id ON user_oauth_identities (user_id);
CREATE UNIQUE INDEX uk_user_oauth_identity ON user_oauth_identities (provider, subject);

package model

// OAuth 身份提供商。provider + subject 唯一，用来把第三方账号稳定关联到本站用户。
const (
	OAuthProviderWatcha = "watcha"
)

// UserOAuthIdentity 是第三方登录身份与本站用户的绑定关系。
// Subject 存第三方用户 ID，不存昵称、头像等会变化的信息；同一第三方账号只能绑定一个本站用户。
type UserOAuthIdentity struct {
	BaseModel
	UserID   uint64 `gorm:"not null;index" json:"user_id"`                                                  // 本站用户 ID
	Provider string `gorm:"size:32;not null;uniqueIndex:uk_user_oauth_identity,priority:1" json:"provider"` // 第三方提供商，如 watcha
	Subject  string `gorm:"size:128;not null;uniqueIndex:uk_user_oauth_identity,priority:2" json:"subject"` // 第三方用户唯一标识
}

// TableName 返回表名。
func (UserOAuthIdentity) TableName() string { return "user_oauth_identities" }

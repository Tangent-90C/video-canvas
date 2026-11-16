package model

import (
	"strconv"
	"time"
)

// UserWallet 用户连接的 TokenPay（TokenDance）钱包：OAuth 授权后拿到的「用户自己的 API Key」的元数据。
// Key 密文存在 ai_secrets（名字 wallet:<user_id>，AES-256-GCM，与渠道 Key 同一套主密钥），这里只存展示与归因信息。
// 每个用户只有一条记录：重新连接覆盖（上游会创建新 Key，旧 Key 由用户在 TokenDance 控制台管理）。
type UserWallet struct {
	UserID    uint64    `gorm:"primaryKey;autoIncrement:false" json:"user_id"` // 所属用户
	Provider  string    `gorm:"size:32;not null" json:"provider"`              // 钱包提供方，固定 tokendance
	AppURL    string    `gorm:"size:512;not null;default:''" json:"app_url"`   // 授权时站点的 App URL（归因标识），展示用
	KeyMasked string    `gorm:"size:64;not null;default:''" json:"key_masked"` // Key 的掩码（连接时记录），展示用；明文只在 ai_secrets
	CreatedAt time.Time `json:"created_at"`                                    // 首次连接时间
	UpdatedAt time.Time `json:"updated_at"`                                    // 最近一次重连时间
}

func (UserWallet) TableName() string { return "user_wallets" }

// WalletSecretName 返回用户钱包 Key 在 ai_secrets 里的名字。用户 ID 不含冒号，与渠道密钥名字不会冲突。
func WalletSecretName(userID uint64) string { return "wallet:" + strconv.FormatUint(userID, 10) }

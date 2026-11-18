package repository

import (
	"context"

	"gorm.io/gorm"

	"video-canvas/internal/model"
)

// GetByOAuth 按第三方提供商和用户标识查询已绑定的本站用户；没有绑定时返回 ErrNotFound。
func (r *UserRepository) GetByOAuth(ctx context.Context, provider, subject string) (*model.User, error) {
	var identity model.UserOAuthIdentity
	if err := r.db.WithContext(ctx).Where("provider = ? AND subject = ?", provider, subject).First(&identity).Error; err != nil {
		return nil, translate(err)
	}
	return r.GetByID(ctx, identity.UserID)
}

// CreateWithOAuth 在一个事务里创建本站用户并写入第三方身份绑定。
// 用户名或身份唯一约束冲突返回 ErrDuplicate；调用方需要先查已有身份，以处理并发登录。
func (r *UserRepository) CreateWithOAuth(ctx context.Context, provider, subject string, u *model.User) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(u).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrDuplicate
			}
			return err
		}
		identity := &model.UserOAuthIdentity{UserID: u.ID, Provider: provider, Subject: subject}
		if err := tx.Create(identity).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrDuplicate
			}
			return err
		}
		return nil
	})
}

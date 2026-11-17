package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"video-canvas/internal/model"
)

// UserWalletRepository 用户钱包（user_wallets）的读写。Key 密文在 ai_secrets，这里只有元数据。
type UserWalletRepository struct {
	db *gorm.DB
}

// NewUserWalletRepository 创建用户钱包仓储。
func NewUserWalletRepository(db *gorm.DB) *UserWalletRepository { return &UserWalletRepository{db: db} }

// GetByUser 取用户连接的钱包；没有连接返回 ErrNotFound。
func (r *UserWalletRepository) GetByUser(ctx context.Context, userID uint64) (*model.UserWallet, error) {
	var row model.UserWallet
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &row, err
}

// Upsert 插入或覆盖用户的钱包记录（重新连接覆盖旧记录）。
func (r *UserWalletRepository) Upsert(ctx context.Context, w *model.UserWallet) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"provider", "app_url", "key_masked", "updated_at"}),
		}).
		Create(w).Error
}

// Delete 删除用户的钱包记录；本来就没有连接返回 ErrNotFound。
func (r *UserWalletRepository) Delete(ctx context.Context, userID uint64) error {
	res := r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&model.UserWallet{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

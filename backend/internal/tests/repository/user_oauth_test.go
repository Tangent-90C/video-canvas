package repository_test

import (
	"context"
	"errors"
	"testing"

	"video-canvas/internal/model"
	"video-canvas/internal/repository"
)

// 观猹身份与本站用户在同一事务里写入，并能按 provider + subject 反查。
func TestUserRepositoryOAuthIdentity(t *testing.T) {
	db := isolatedDB(t, &model.User{}, &model.UserOAuthIdentity{})
	repo := repository.NewUserRepository(db)
	ctx := context.Background()
	user := &model.User{Username: "watcha_12345", Password: "hash", Role: model.RoleUser, Status: model.UserStatusActive}
	if err := repo.CreateWithOAuth(ctx, model.OAuthProviderWatcha, "12345", user); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByOAuth(ctx, model.OAuthProviderWatcha, "12345")
	if err != nil || got.ID != user.ID || got.Username != user.Username {
		t.Fatalf("反查身份失败：%+v %v", got, err)
	}
	other := &model.User{Username: "other", Password: "hash", Role: model.RoleUser, Status: model.UserStatusActive}
	if err := repo.CreateWithOAuth(ctx, model.OAuthProviderWatcha, "12345", other); !errors.Is(err, repository.ErrDuplicate) {
		t.Fatalf("重复身份应返回 ErrDuplicate：%v", err)
	}
	if _, err := repo.GetByOAuth(ctx, model.OAuthProviderWatcha, "missing"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("未绑定身份应返回 ErrNotFound：%v", err)
	}
}

package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	. "video-canvas/internal/handler"
	"video-canvas/internal/model"
	"video-canvas/internal/service"
)

type fakeWatchaService struct {
	next string
	meta service.ClientMeta
}

func (f *fakeWatchaService) Start(_ context.Context, next string) (*model.WatchaAuthStartView, error) {
	f.next = next
	return &model.WatchaAuthStartView{AuthURL: "https://watcha.example.com/oauth/authorize?state=s"}, nil
}

func (f *fakeWatchaService) Callback(_ context.Context, state, code string, meta service.ClientMeta) (*model.WatchaLoginView, error) {
	f.meta = meta
	return &model.WatchaLoginView{Token: "jwt", ExpireAt: 1, Role: model.RoleUser, Next: state + "/" + code}, nil
}

func TestWatchaHandler_StartAndCallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeWatchaService{}
	h := NewWatchaHandler(fake)
	r := gin.New()
	r.GET("/api/v1/auth/watcha/start", h.Start)
	r.POST("/api/v1/auth/watcha/callback", h.Callback)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/watcha/start?next=%2Fcanvas%2F1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "watcha.example.com") || fake.next != "/canvas/1" {
		t.Fatalf("发起授权响应不对：code=%d body=%s next=%q", rec.Code, rec.Body.String(), fake.next)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/watcha/callback", strings.NewReader(`{"flow":"state","code":"code"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ua")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var body struct {
		Code int                   `json:"code"`
		Data model.WatchaLoginView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK || body.Data.Token != "jwt" || fake.meta.UserAgent != "ua" {
		t.Fatalf("回调响应不对：code=%d body=%s err=%v", rec.Code, rec.Body.String(), err)
	}
}

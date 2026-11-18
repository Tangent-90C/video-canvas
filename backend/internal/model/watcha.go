package model

// WatchaAuthStartView 是发起观猹 OAuth 登录的响应。
type WatchaAuthStartView struct {
	AuthURL string `json:"auth_url"` // 观猹授权页地址，前端整页跳转
}

// WatchaCallbackReq 是观猹授权回调交换登录态的请求体。
type WatchaCallbackReq struct {
	Flow string `json:"flow" binding:"required,max=256" label:"授权流程"` // 发起登录时生成的一次性 state
	Code string `json:"code" binding:"required,max=512" label:"授权码"`  // 观猹返回的一次性 code
}

// WatchaLoginView 是观猹登录成功后的响应；Next 是发起登录时记录的站内落点。
type WatchaLoginView struct {
	Token    string `json:"token"`     // JWT
	ExpireAt int64  `json:"expire_at"` // 过期时间（Unix 秒）
	Role     string `json:"role"`      // 用户角色
	Next     string `json:"next"`      // 登录后跳转的站内路径
}

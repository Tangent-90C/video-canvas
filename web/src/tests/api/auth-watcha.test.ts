import { afterEach, beforeEach, describe, expect, spyOn, test, type Mock } from "bun:test";

import { completeWatchaLogin, startWatchaLogin } from "@/api/auth";
import service from "@/utils/requests/service";

describe("观猹 OAuth 登录 API", () => {
  let get: Mock<typeof service.get>;
  let post: Mock<typeof service.post>;

  beforeEach(() => {
    get = spyOn(service, "get") as unknown as Mock<typeof service.get>;
    post = spyOn(service, "post") as unknown as Mock<typeof service.post>;
  });

  afterEach(() => {
    get.mockRestore();
    post.mockRestore();
  });

  test("发起登录把站内落点交给后端并返回授权地址", async () => {
    get.mockResolvedValue({ auth_url: "https://watcha.cn/oauth/authorize?state=s" } as never);
    await expect(startWatchaLogin("/canvas/1")).resolves.toEqual({
      auth_url: "https://watcha.cn/oauth/authorize?state=s",
    });
    expect(get).toHaveBeenCalledWith("/auth/watcha/start", { next: "/canvas/1" });
  });

  test("回调只用 state 和一次性 code 换 JWT", async () => {
    post.mockResolvedValue({ token: "jwt", expire_at: 1, role: "user", next: "/" } as never);
    await completeWatchaLogin("state", "code");
    expect(post).toHaveBeenCalledWith("/auth/watcha/callback", { flow: "state", code: "code" });
  });
});

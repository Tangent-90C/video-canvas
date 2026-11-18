import { describe, expect, test } from "bun:test";
import { existsSync } from "node:fs";
import { renderToStaticMarkup } from "react-dom/server";

import { LOGIN_FEATURE_SLIDES, LoginFeatureCarousel } from "@/pages/login/login-feature-carousel";

describe("登录页：功能截图轮播", () => {
  test("首帧展示产品首页，并提供可访问的轮播、分页和暂停控件", () => {
    const out = renderToStaticMarkup(<LoginFeatureCarousel />);

    expect(out).toContain('data-slot="login-feature-carousel"');
    expect(out).toContain('aria-roledescription="carousel"');
    expect(out).toContain('aria-label="连镜功能预览"');
    expect(out).toContain('aria-roledescription="slide"');
    expect(out).toContain("首页");
    expect(out).toContain('src="/login-showcase/home.webp"');
    expect(out).toContain('aria-label="暂停功能预览轮播"');
    expect(out).toContain('aria-label="查看第 2 个功能：画布"');
    expect(out).toContain('aria-current="true"');
  });

  test("所有轮播截图都来自 docs/assets/screenshots 并已放入可构建的 public 目录", () => {
    expect(LOGIN_FEATURE_SLIDES.length).toBeGreaterThanOrEqual(4);
    for (const slide of LOGIN_FEATURE_SLIDES) {
      expect(slide.image.startsWith("/login-showcase/")).toBe(true);
      expect(existsSync(new URL(`../../../../public${slide.image}`, import.meta.url))).toBe(true);
    }
  });
});

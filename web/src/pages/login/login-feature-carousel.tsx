import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { ChevronLeft, ChevronRight, Pause, Play } from "lucide-react";

import { DURATION, EASE_OUT, TAP } from "@/lib/motion";
import { cn } from "@/lib/utils";

/** 每条功能截图自动停留的时间（毫秒） */
const AUTO_ADVANCE_MS = 6500;

/** 登录页轮播的一张产品功能截图 */
export interface LoginFeatureSlide {
  /** 稳定标识，用于图片切换动画 */
  id: string;
  /** 功能名称 */
  title: string;
  /** 一句话说明这个功能能做什么 */
  description: string;
  /** 从 public 根目录开始的截图地址 */
  image: string;
  /** 给读屏使用的截图说明 */
  imageAlt: string;
}

/**
 * 登录页展示的产品功能顺序：先给用户看到创作入口，再展开画布、短剧、导演台和营销能力。
 * 图片由 docs/assets/screenshots 压缩后放在 public，登录页不直接依赖仓库文档目录。
 */
export const LOGIN_FEATURE_SLIDES: readonly LoginFeatureSlide[] = [
  {
    id: "home",
    title: "首页",
    description: "写下脑海里的画面，从一句话开始创作。",
    image: "/login-showcase/home.webp",
    imageAlt: "连镜首页，中间是创作输入框，背景展示生成的电影画面。",
  },
  {
    id: "canvas",
    title: "画布",
    description: "文本、图片和视频在同一张无限画布上连成故事。",
    image: "/login-showcase/canvas.webp",
    imageAlt: "连镜无限画布，多个图片和视频节点通过连线组成分镜流程。",
  },
  {
    id: "drama-studio",
    title: "短剧工作室",
    description: "剧本、分镜、角色和素材集中管理，连续推进整部短剧。",
    image: "/login-showcase/drama-studio.webp",
    imageAlt: "短剧工作室总览，展示剧集统计、制作进度和分集列表。",
  },
  {
    id: "director",
    title: "3D 导演台",
    description: "先摆人物与机位，用空间关系把镜头调度想清楚。",
    image: "/login-showcase/director.webp",
    imageAlt: "连镜 3D 导演台，在虚拟场景中摆放人物和摄影机。",
  },
  {
    id: "marketing-studio",
    title: "营销工作室",
    description: "从商品素材到卖点脚本与视频画面，一次完成。",
    image: "/login-showcase/marketing-studio.webp",
    imageAlt: "营销工作室，左侧是商品素材，右侧由 Agent 生成营销内容。",
  },
];

/**
 * 登录页的功能截图轮播：始终占满页面宽度，桌面端截图与说明并排，移动端说明放在截图下方。
 * 自动轮播会在悬停、键盘聚焦、页面隐藏或用户主动暂停时停下；减少动态效果时默认不自动切换。
 * @param active 是否自动轮播；展开登录表单时暂停自动切换，但轮播和手动翻页仍然可用
 */
export function LoginFeatureCarousel({ active = true }: { active?: boolean }) {
  const reducedMotion = useReducedMotion() ?? false;
  const [index, setIndex] = useState(0);
  const [paused, setPaused] = useState(false);
  const [interactionPaused, setInteractionPaused] = useState(false);
  const [pageHidden, setPageHidden] = useState(false);
  const frameRef = useRef<HTMLElement>(null);
  const slideId = useId();
  const count = LOGIN_FEATURE_SLIDES.length;
  const slide = LOGIN_FEATURE_SLIDES[index] as LoginFeatureSlide;
  const autoAdvance = active && !reducedMotion && !paused && !interactionPaused && !pageHidden;

  useEffect(() => {
    const images = LOGIN_FEATURE_SLIDES.map((item) => {
      const image = new Image();
      image.src = item.image;
      return image;
    });
    return () => images.forEach((image) => image.removeAttribute("src"));
  }, []);

  useEffect(() => {
    const onChange = () => setPageHidden(document.hidden);
    onChange();
    document.addEventListener("visibilitychange", onChange);
    return () => document.removeEventListener("visibilitychange", onChange);
  }, []);

  useEffect(() => {
    if (!autoAdvance) return;
    const timer = window.setTimeout(
      () => setIndex((value) => (value + 1) % count),
      AUTO_ADVANCE_MS,
    );
    return () => window.clearTimeout(timer);
  }, [autoAdvance, count, index]);

  const goTo = (target: number) => setIndex((target + count) % count);

  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      goTo(index - 1);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      goTo(index + 1);
    }
  };

  return (
    <section
      ref={frameRef}
      data-slot="login-feature-carousel"
      data-layout="full-bleed"
      role="region"
      aria-roledescription="carousel"
      aria-label="连镜功能预览"
      tabIndex={0}
      onKeyDown={onKeyDown}
      onMouseEnter={() => setInteractionPaused(true)}
      onMouseLeave={() => setInteractionPaused(false)}
      onFocusCapture={() => setInteractionPaused(true)}
      onBlurCapture={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
          setInteractionPaused(false);
        }
      }}
      className="bg-stage-glass border-stage-glass-border relative w-full overflow-hidden border-y text-left shadow-[0_24px_70px_-38px_oklch(0_0_0/0.9)] backdrop-blur-xl outline-none focus-visible:ring-2 focus-visible:ring-white/45 focus-visible:ring-inset"
    >
      <div className="grid w-full lg:grid-cols-[minmax(0,1fr)_clamp(320px,26vw,460px)]">
        <div className="bg-stage relative aspect-[4/3] overflow-hidden sm:aspect-[16/9] lg:aspect-[1.846/1]">
          <AnimatePresence initial={false} mode="wait">
            <motion.img
              key={slide.id}
              src={slide.image}
              alt={slide.imageAlt}
              width={1440}
              height={slide.id === "director" ? 681 : 780}
              initial={reducedMotion ? false : { opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={reducedMotion ? undefined : { opacity: 0 }}
              transition={{ duration: reducedMotion ? 0 : DURATION.slow, ease: EASE_OUT }}
              className="absolute inset-0 size-full object-cover object-top"
            />
          </AnimatePresence>
          <div className="pointer-events-none absolute inset-0 bg-[linear-gradient(to_top,oklch(0.09_0_0/0.58),transparent_32%)]" />
          <div className="absolute top-4 left-4 rounded-full border border-white/14 bg-black/38 px-3 py-1.5 backdrop-blur-md md:top-6 md:left-6">
            <span className="text-on-stage-muted text-[11px] tracking-[0.16em] uppercase">
              功能 {String(index + 1).padStart(2, "0")} / {String(count).padStart(2, "0")}
            </span>
          </div>
        </div>

        <div className="border-stage-glass-border/70 flex flex-col justify-between gap-10 border-t bg-black/12 p-6 sm:p-8 lg:border-t-0 lg:border-l lg:p-10 xl:p-12">
          <div>
            <span className="text-on-stage-muted text-[11px] tracking-[0.16em] uppercase">
              功能 {String(index + 1).padStart(2, "0")} / {String(count).padStart(2, "0")}
            </span>
            <h2 className="text-on-stage mt-3 text-2xl font-semibold tracking-tight sm:text-3xl xl:text-4xl">
              {slide.title}
            </h2>
            <p className="text-on-stage-muted mt-4 max-w-[38ch] text-sm leading-7 sm:text-base">
              {slide.description}
            </p>
          </div>
          <div className="text-on-stage-muted flex items-center gap-2 text-xs">
            <span className="size-1.5 rounded-full bg-current" />
            {autoAdvance ? "自动轮播" : "已暂停"}
          </div>
        </div>
      </div>

      <div className="border-stage-glass-border/70 flex min-h-14 items-center justify-between gap-3 border-t bg-black/16 px-3 py-2 md:px-6">
        <div
          className="flex min-w-0 items-center gap-1 overflow-x-auto"
          role="group"
          aria-label="选择功能"
        >
          {LOGIN_FEATURE_SLIDES.map((item, dotIndex) => (
            <button
              key={item.id}
              type="button"
              aria-label={`查看第 ${dotIndex + 1} 个功能：${item.title}`}
              aria-controls={slideId}
              aria-current={dotIndex === index}
              onClick={() => goTo(dotIndex)}
              className={cn(
                "focus-visible:ring-on-stage/60 shrink-0 rounded-full px-3 py-2 text-xs font-medium whitespace-nowrap transition-colors outline-none focus-visible:ring-2 md:text-sm",
                dotIndex === index
                  ? "bg-stage-glass text-on-stage"
                  : "text-on-stage-muted hover:text-on-stage hover:bg-white/6",
              )}
            >
              {item.title}
            </button>
          ))}
        </div>

        <div className="flex shrink-0 items-center gap-1">
          <motion.button
            type="button"
            whileTap={TAP}
            onClick={() => goTo(index - 1)}
            aria-label="上一个功能"
            className="text-on-stage-muted hover:text-on-stage hover:bg-stage-glass focus-visible:ring-on-stage/60 grid size-10 place-items-center rounded-full outline-none focus-visible:ring-2"
          >
            <ChevronLeft className="size-4.5" />
          </motion.button>
          <motion.button
            type="button"
            whileTap={TAP}
            onClick={() => goTo(index + 1)}
            aria-label="下一个功能"
            className="text-on-stage-muted hover:text-on-stage hover:bg-stage-glass focus-visible:ring-on-stage/60 grid size-10 place-items-center rounded-full outline-none focus-visible:ring-2"
          >
            <ChevronRight className="size-4.5" />
          </motion.button>
          {!reducedMotion && (
            <motion.button
              type="button"
              whileTap={TAP}
              onClick={() => setPaused((value) => !value)}
              aria-label={paused ? "播放功能预览轮播" : "暂停功能预览轮播"}
              title={paused ? "播放功能预览轮播" : "暂停功能预览轮播"}
              className="text-on-stage-muted hover:text-on-stage hover:bg-stage-glass focus-visible:ring-on-stage/60 ml-1 grid size-10 place-items-center rounded-full outline-none focus-visible:ring-2"
            >
              {paused ? <Play className="size-4 fill-current" /> : <Pause className="size-4" />}
            </motion.button>
          )}
        </div>
      </div>

      <div
        id={slideId}
        role="group"
        aria-roledescription="slide"
        aria-label={`第 ${index + 1} 个功能，共 ${count} 个：${slide.title}`}
        aria-live={autoAdvance ? "off" : "polite"}
        aria-atomic="true"
        className="sr-only"
      >
        {slide.title}。{slide.description}
      </div>
    </section>
  );
}

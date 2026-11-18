/**
 * TokenDance（词元跳动）· 图片插件（内置，契约 v1）。
 * 对接 TokenDance 网关的方舟图像接口（渠道 base_url 填 https://tokendance.space/gateway）：
 *   POST /ark/v3/images/generations（同步），成图 URL 有效期 24 小时，宿主会转存。
 * 鉴权：Authorization: Bearer <TokenDance API Key>，由宿主注入；TokenPay 钱包渠道注入连接钱包的用户自己的 Key，普通渠道注入该渠道自己的 Key。
 * 上游模型即 TokenDance 目录里 supported_protocols 含 ark:image-generations / openai:image-generations 的模型：
 *   seedream-5.0-lite、qwen-image-2.1-pro 等（以 https://tokendance.space/gateway/v1/models 实时目录为准）。
 *
 * 与 OpenAI Images 协议（openai-image.js）的差异——这个网关不是 OpenAI 语义，直接发 "1024x1024" 会被拒：
 *   - size 是「宽x高」像素制且**必须至少 3686400 像素**（1920x1920，即 2K 起步）；1K 及以下的尺寸上游 400
 *   - 没有 quality / style / n 字段；张数由宿主按生成数量拆成多次请求，插件一次只出一张
 *   - 图生图 / 多参考图不走 /images/edits，而是同一个路径加 image 字段（单条 URL 或 URL 数组）
 *   - 固定 response_format:"url"（宿主按 URL 转存）、watermark:false（不带上游水印）
 *
 * 写法说明：goja 只保证 ES5.1 + 部分 ES6，所以这里用 var / function，不用可选链与空值合并。
 *
 * 用户输入（宿主按模型能力校验后传入）：prompt、op（t2i / i2i）、images（参考图，文件引用）、
 *   aspect_ratio（比例，换算成合法尺寸）、size（显式尺寸，给了就用但仍要过最小像素校验）、
 *   resolution 本接口没有对应概念，忽略（尺寸由 aspect_ratio 决定）。
 */
module.exports = {
  meta: {
    apiVersion: 1,
    key: "tokendance-image",
    name: "TokenDance · 图片",
    version: "1.0.0",
    vendor: "tokendance",
    description: "TokenDance（词元跳动）Seedream 图像生成（同步，2K 起步）",
    auth: { type: "bearer" },
    endpoints: {
      image: { mode: "sync", method: "POST", path: "/ark/v3/images/generations" },
    },
  },

  buildSubmitRequest: function (ctx) {
    return buildGenerateRequest(ctx);
  },

  // 同步协议：响应形状与 OpenAI 兼容（{data:[{url}]}），一次出一张；结果包在 immediate 里
  parseSubmitResponse: function (ctx, resp) {
    return { immediate: parseImageResponse(resp.body || {}) };
  },

  classifyError: function (ctx, resp) {
    var msg = errorMessageOf(resp.body) || (resp.text || "").slice(0, 300);
    var code = errorCodeOf(resp.body);
    if (!msg && !code) {
      return null;
    }
    var cls = classifyText(code + " " + msg);
    if (cls === "terminal") {
      return null; // 其余交给宿主默认规则（429 / 5xx 重试，其它 terminal）
    }
    return { class: cls, code: code || undefined, message: msg || code };
  },
};

var TASKS_PATH = "/ark/v3/images/generations";

/** 网关的最小像素数：3686400 = 1920x1920（2K 起步），低于它的尺寸一律 400 */
var MIN_PIXELS = 3686400;

/**
 * 常见比例的合法尺寸（全部 >= MIN_PIXELS）。画布参数面板的比例选项（Auto/16:9/4:3/1:1/3:4/9:16/21:9）都能命中；
 * 没命中的比例按比例现算（见 sizeForRatio）。
 */
var RATIO_SIZES = {
  auto: "2048x2048",
  "1:1": "2048x2048",
  "16:9": "2560x1440",
  "9:16": "1440x2560",
  "4:3": "2240x1680",
  "3:4": "1680x2240",
  "21:9": "2940x1260",
};

// ---------------------------------------------------------------------------
// 生图
// ---------------------------------------------------------------------------

function buildGenerateRequest(ctx) {
  var input = ctx.input || {};
  var model = ctx.model.upstreamModel;
  var prompt = requirePrompt(input, model);

  var body = { model: model, prompt: prompt };
  var size = pickSize(input, model);
  if (size) {
    body.size = size;
  }
  var images = refList(input.images);
  if (images.length === 1) {
    // 单张参考图：ark 用 image 单值
    body.image = { __fileRef: images[0], as: "url" };
  } else if (images.length > 1) {
    // 多参考图：image 数组（多主体参考生图）
    body.image = images.map(function (ref) {
      return { __fileRef: ref, as: "url" };
    });
  }
  body.response_format = "url"; // 宿主按 URL 转存，不让上游回 base64
  body.watermark = false; // 不带上游水印
  return { method: "POST", path: TASKS_PATH, json: body, responseType: "image_json", timeout: 120 };
}

function requirePrompt(input, model) {
  var prompt = input.prompt === undefined || input.prompt === null ? "" : String(input.prompt);
  if (prompt === "") {
    throw new Error(model + " 需要提示词");
  }
  return prompt;
}

/** 参考图文件引用列表 */
function refList(images) {
  if (!isArray(images)) {
    return [];
  }
  return images.filter(function (ref) {
    return typeof ref === "string" && ref !== "";
  });
}

/**
 * 选尺寸：用户显式给了 size 就校验后用；否则按 aspect_ratio 换算。
 * 显式尺寸低于最小像素时直接抛错（给一句人能看懂的话），不发出去让上游 400。
 */
function pickSize(input, model) {
  var explicit = firstString(input.size);
  if (explicit) {
    var w = 0;
    var h = 0;
    var m = /^(\d+)\s*[x×]\s*(\d+)$/i.exec(explicit);
    if (!m) {
      // 档位制（如 "2K"）原样透传，由上游解释
      return explicit;
    }
    w = parseInt(m[1], 10);
    h = parseInt(m[2], 10);
    if (w * h < MIN_PIXELS) {
      throw new Error(
        model + " 的尺寸不能小于 " + MIN_PIXELS + " 像素（约 1920x1920）：" + explicit + " 太小了",
      );
    }
    return w + "x" + h;
  }
  var ratio = normalizeRatio(input.aspect_ratio);
  if (RATIO_SIZES[ratio]) {
    return RATIO_SIZES[ratio];
  }
  // 没给比例或比例不在表里：用模型默认（不發 size，让网关自己定，它一定合法）
  return "";
}

/** 归一化比例串："16:9" / "16 : 9" / "auto" 都认 */
function normalizeRatio(value) {
  var s = firstString(value).toLowerCase().replace(/\s+/g, "");
  if (s === "" || s === "auto" || s === "adaptive") {
    return "auto";
  }
  var m = /^(\d+):(\d+)$/.exec(s);
  if (!m) {
    return "";
  }
  var w = parseInt(m[1], 10);
  var h = parseInt(m[2], 10);
  if (!w || !h) {
    return "";
  }
  // 表里没有的比例按比例现算一个合法尺寸（短边对准，保证 >= MIN_PIXELS），取 16 的倍数方便上游编码
  var key = w + ":" + h;
  if (!RATIO_SIZES[key]) {
    var scale = Math.sqrt(MIN_PIXELS / (w * h));
    RATIO_SIZES[key] = round16(w * scale) + "x" + round16(h * scale);
  }
  return key;
}

function round16(v) {
  var n = Math.max(16, Math.round(v));
  return Math.round(n / 16) * 16;
}

// ---------------------------------------------------------------------------
// 响应解析
// ---------------------------------------------------------------------------

/** data[].url 作为 url 产物交给宿主转存；没有图片时按失败处理 */
function parseImageResponse(body) {
  var list = isArray(body.data) ? body.data : [];
  for (var i = 0; i < list.length; i++) {
    var item = list[i];
    if (item && typeof item.url === "string" && /^https?:\/\//i.test(item.url)) {
      return { status: "succeeded", outputs: [{ type: "url", url: item.url, media_type: "image", mime: mimeOfImage(item.url) }] };
    }
  }
  var msg = errorMessageOf(body);
  if (msg) {
    return { status: "failed", error: { class: classifyText(msg), message: msg, code: errorCodeOf(body) || undefined } };
  }
  return { status: "failed", error: { class: "terminal", message: "上游没有返回图片" } };
}

function mimeOfImage(url) {
  var m = /\.([a-z0-9]{2,5})(?:\?|#|$)/i.exec(url);
  var ext = m ? m[1].toLowerCase() : "";
  if (ext === "jpg" || ext === "jpeg") {
    return "image/jpeg";
  }
  if (ext === "webp") {
    return "image/webp";
  }
  if (ext === "png") {
    return "image/png";
  }
  return "image/jpeg";
}

// ---------------------------------------------------------------------------
// 通用小工具
// ---------------------------------------------------------------------------

function firstString(value) {
  return typeof value === "string" ? value.trim() : "";
}

function isArray(v) {
  return Object.prototype.toString.call(v) === "[object Array]";
}

/** 按错误码 + 文案判断失败分类：审核 → moderation；余额 / 额度 → provider_balance；限流 → retryable；其余 terminal。 */
function classifyText(text) {
  var s = String(text || "").toLowerCase();
  if (/sensitive|moderation|contentsecurity|content[ _-]?policy|violat|违规|审核|敏感/.test(s)) {
    return "moderation";
  }
  if (/overdue|insufficient|balance|quota|setlimitexceeded|credit|欠费|余额|额度/.test(s)) {
    return "provider_balance";
  }
  if (/ratelimit|rate limit|overloaded|toomanyrequests|限流|过载/.test(s)) {
    return "retryable";
  }
  return "terminal";
}

/** 上游错误体：{error:{code,message}} */
function errorMessageOf(body) {
  if (!body || typeof body !== "object") {
    return "";
  }
  var e = body.error;
  if (e && typeof e === "object" && typeof e.message === "string" && e.message) {
    return e.message;
  }
  if (typeof e === "string" && e) {
    return e;
  }
  return "";
}

function errorCodeOf(body) {
  if (!body || typeof body !== "object") {
    return "";
  }
  var e = body.error;
  return e && typeof e === "object" && typeof e.code === "string" ? e.code : "";
}

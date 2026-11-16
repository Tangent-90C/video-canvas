/**
 * TokenDance（词元跳动）· 视频插件（内置，契约 v1）。
 * 基于 doubao-video.js（同为方舟 Seedance 协议）改写：仅请求路径不同。
 *
 * 直连 TokenDance 网关的 Seedance 接口（渠道 base_url 填 https://tokendance.space/gateway）：
 *   POST /ark/v3/generations/tasks 提交（返回 {id}），GET /ark/v3/generations/tasks/{id} 轮询，异步出片；
 *   状态 queued / running / succeeded / failed，成功后 content.video_url 是成片地址（有效期 24 小时，宿主会转存）。
 * 鉴权：Authorization: Bearer <TokenDance API Key>，由宿主按渠道 Key 注入；钱包渠道注入的是连接钱包的用户自己的 Key。
 * 上游模型（ctx.model.upstreamModel）即 TokenDance 模型目录里的 Seedance 模型 ID：seedance-2.0、seedance-2.5-flash、
 *   seedance-2.5 等（以 https://tokendance.space/gateway/v1/models 实时目录为准）。
 *
 * 与方舟直连的差异：路径 /ark/v3/...（网关统一前缀）；其余请求/响应形状与方舟 Seedance 一致
 * （content 数组 + ratio / resolution / duration / generate_audio / watermark，参考素材角色 first_frame / last_frame /
 *   reference_image / reference_video / reference_audio）。参数取值范围交给上游 400，分辨率 / 时长由运营在模型能力里配置。
 *
 * 写法说明：goja 只保证 ES5.1 + 部分 ES6，所以这里用 var / function，不用可选链与空值合并。
 *
 * 用户输入（宿主按模型能力校验后传入）：与 doubao-video 相同——
 *   prompt、op、images / videos / audios（参考素材，文件引用换成签名 URL）、
 *   aspect_ratio -> ratio（如 16:9、adaptive）、resolution、duration（-1 让模型自定时长）、generate_audio；
 *   水印固定为 false；素材角色：op=omni 或图片多于 2 张按参考图，2 张是首帧 + 尾帧，1 张是首帧（不写 role）；
 *   videos -> reference_video，audios -> reference_audio
 */
module.exports = {
  meta: {
    apiVersion: 1,
    key: "tokendance-video",
    name: "TokenDance · 视频",
    version: "1.0.0",
    vendor: "tokendance",
    description: "TokenDance（词元跳动）Seedance 视频生成（异步）",
    auth: { type: "bearer" },
    endpoints: {
      video: { mode: "async", method: "POST", path: "/ark/v3/generations/tasks" },
    },
    poll: { firstDelay: 15, interval: 10, maxInterval: 20, jitter: 0.2 },
  },

  buildSubmitRequest: function (ctx) {
    return buildVideoRequest(ctx);
  },

  parseSubmitResponse: function (ctx, resp) {
    var body = resp.body || {};
    if (body.id === undefined || body.id === null || body.id === "") {
      var msg = errorMessageOf(body);
      if (msg) {
        return { immediate: failed(msg, errorCodeOf(body)) };
      }
      throw new Error("上游没有返回任务 id");
    }
    return { providerTaskId: String(body.id) };
  },

  buildQueryRequest: function (ctx) {
    return {
      method: "GET",
      path: TASKS_PATH + "/" + encodeURIComponent(ctx.task.providerTaskId),
      timeout: 30,
    };
  },

  parseQueryResponse: function (ctx, resp) {
    var body = resp.body || {};
    switch (String(body.status || "").toLowerCase()) {
      case "succeeded":
        var content = body.content || {};
        if (typeof content.video_url !== "string" || content.video_url === "") {
          return failed("上游返回成功，但没有视频地址");
        }
        return {
          status: "succeeded",
          progress: 100,
          outputs: [{ type: "url", url: content.video_url, media_type: "video", mime: "video/mp4" }],
        };
      case "failed":
        return failed(errorMessageOf(body) || "上游任务失败", errorCodeOf(body));
      case "expired":
        return failed(errorMessageOf(body) || "上游任务超时未完成（expired）", errorCodeOf(body) || "expired");
      case "cancelled":
        return failed(errorMessageOf(body) || "上游任务已被取消", errorCodeOf(body) || "cancelled");
      case "running":
      case "processing":
        return { status: "running" };
      default:
        // queued / pending / 未知：继续等待，由任务 deadline 兜底
        return { status: "queued" };
    }
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

var TASKS_PATH = "/ark/v3/generations/tasks";

// ---------------------------------------------------------------------------
// 视频（Seedance）
// ---------------------------------------------------------------------------

function buildVideoRequest(ctx) {
  var input = ctx.input || {};
  var body = { model: ctx.model.upstreamModel, content: videoContent(input) };

  setIf(body, "ratio", input.aspect_ratio);
  setIf(body, "resolution", input.resolution);
  if (typeof input.duration === "number") {
    body.duration = input.duration; // -1 表示让模型自己定时长
  }
  if (typeof input.generate_audio === "boolean") {
    body.generate_audio = input.generate_audio;
  }
  body.watermark = false;
  return { method: "POST", path: TASKS_PATH, json: body, timeout: 60 };
}

// content 数组：文本在前，随后是图片 / 视频 / 音频参考。素材都用签名 URL 交给上游拉取。
function videoContent(input) {
  var content = [];
  if (input.prompt !== undefined && String(input.prompt) !== "") {
    content.push({ type: "text", text: String(input.prompt) });
  }
  var images = isArray(input.images) ? input.images : [];
  for (var i = 0; i < images.length; i++) {
    var item = { type: "image_url", image_url: { url: { __fileRef: images[i], as: "url" } } };
    var role = imageRole(input.op, images.length, i);
    if (role) {
      item.role = role;
    }
    content.push(item);
  }
  var videos = isArray(input.videos) ? input.videos : [];
  for (var v = 0; v < videos.length; v++) {
    content.push({
      type: "video_url",
      video_url: { url: { __fileRef: videos[v], as: "url" } },
      role: "reference_video",
    });
  }
  var audios = isArray(input.audios) ? input.audios : [];
  for (var a = 0; a < audios.length; a++) {
    content.push({
      type: "audio_url",
      audio_url: { url: { __fileRef: audios[a], as: "url" } },
      role: "reference_audio",
    });
  }
  return content;
}

// 图片在视频里的角色：全能参考与多于 2 张都是参考图；2 张是首帧 + 尾帧；1 张是首帧（不写 role）。
function imageRole(op, total, index) {
  if (op === "omni" || total > 2) {
    return "reference_image";
  }
  if (total === 2) {
    return index === 0 ? "first_frame" : "last_frame";
  }
  return "";
}

// ---------------------------------------------------------------------------
// 通用小工具
// ---------------------------------------------------------------------------

function failed(message, code) {
  var err = { class: classifyText((code || "") + " " + message), message: message };
  if (code) {
    err.code = code;
  }
  return { status: "failed", error: err };
}

// 按错误码 + 文案判断失败分类：审核 → moderation；余额 / 额度 / 欠费 → provider_balance；限流 / 过载 → retryable；其余 terminal。
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

// 上游错误体：{error:{code,message}}（任务查询里 failed 任务同样带 error）。
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

function setIf(body, key, value) {
  if (value !== undefined && value !== null && value !== "") {
    body[key] = value;
  }
}

function isArray(v) {
  return Object.prototype.toString.call(v) === "[object Array]";
}

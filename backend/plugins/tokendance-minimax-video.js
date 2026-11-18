/**
 * TokenDance（词元跳动）· MiniMax 视频 V2 插件（内置，契约 v1）。
 *
 * POST /minimax/v2/video_generation 提交，GET /minimax/v2/query/video_generation/{id} 查询。
 * content 数组支持 text / image_url / video_url / audio_url；纯文本时 ratio 必填且不能 adaptive，
 * 首帧 / 首尾帧时比例由图片决定。鉴权由宿主注入用户自己的 TokenPay Key。
 */
module.exports = {
  meta: {
    apiVersion: 1,
    key: "tokendance-minimax-video",
    name: "TokenDance · MiniMax 视频",
    version: "1.0.0",
    vendor: "minimax",
    description: "TokenDance MiniMax 视频 V2：文生、首尾帧和多模态参考（异步）",
    auth: { type: "bearer" },
    endpoints: { video: { mode: "async", method: "POST", path: "/minimax/v2/video_generation" } },
    poll: { firstDelay: 20, interval: 10, maxInterval: 20, jitter: 0.2 },
  },

  buildSubmitRequest: function (ctx) {
    var input = ctx.input || {};
    var body = {
      model: ctx.model.upstreamModel,
      content: contentOf(input),
      duration: durationOf(input),
      resolution: resolutionOf(input),
    };
    if (input.op === "t2v") {
      body.ratio = ratioOf(input.aspect_ratio, false);
    } else if (input.op === "omni" && textOf(input.aspect_ratio)) {
      body.ratio = ratioOf(input.aspect_ratio, true);
    }
    return { method: "POST", path: "/minimax/v2/video_generation", json: body, timeout: 60 };
  },

  parseSubmitResponse: function (ctx, resp) {
    var body = resp.body || {};
    var id = body.task_id || (body.task && body.task.id);
    if (id === undefined || id === null || id === "") {
      if (body.base_resp && body.base_resp.status_msg) {
        return { immediate: failed(body.base_resp.status_code, body.base_resp.status_msg) };
      }
      throw new Error("上游没有返回 MiniMax 任务 ID");
    }
    return { providerTaskId: String(id) };
  },

  buildQueryRequest: function (ctx) {
    return {
      method: "GET",
      path: "/minimax/v2/query/video_generation/" + encodeURIComponent(ctx.task.providerTaskId),
      timeout: 30,
    };
  },

  parseQueryResponse: function (ctx, resp) {
    var body = resp.body || {};
    var task = body.task || {};
    var status = String(task.status || body.status || "").toLowerCase();
    if (status === "succeeded" || status === "success") {
      var content = task.content || {};
      var url = typeof content.url === "string" ? content.url : "";
      if (!url) return failed("", "上游返回成功，但没有视频地址");
      return {
        status: "succeeded",
        progress: 100,
        outputs: [{ type: "url", url: url, media_type: "video", mime: "video/mp4" }],
      };
    }
    if (status === "failed" || status === "cancelled" || status === "canceled" || status === "expired") {
      var err = task.error || body.error || {};
      return failed(err.code || (body.base_resp && body.base_resp.status_code), err.message || (body.base_resp && body.base_resp.status_msg) || "MiniMax 任务失败");
    }
    if (status === "running" || status === "processing") return { status: "running", progress: 50 };
    return { status: "queued" };
  },

  classifyError: function (ctx, resp) {
    var body = resp.body && typeof resp.body === "object" ? resp.body : {};
    var base = body.base_resp || {};
    var err = body.error || {};
    var message = err.message || base.status_msg || body.message || (resp.text || "").slice(0, 300);
    var code = err.code || base.status_code || "";
    var cls = classifyText(String(code) + " " + message);
    if (cls === "terminal") return null;
    return { class: cls, code: code ? String(code) : undefined, message: message || undefined };
  },
};

function contentOf(input) {
  var content = [];
  if (textOf(input.prompt)) content.push({ type: "text", text: textOf(input.prompt) });
  var images = refList(input.images);
  var videos = refList(input.videos);
  var audios = refList(input.audios);
  var omni = input.op === "omni" || videos.length > 0 || audios.length > 0;
  for (var i = 0; i < images.length; i++) {
    var role = omni ? "reference_image" : i === 0 ? "first_frame" : "last_frame";
    content.push({ type: "image_url", image_url: { url: refURL(images[i]) }, role: role });
  }
  for (var v = 0; v < videos.length; v++) {
    content.push({ type: "video_url", video_url: { url: refURL(videos[v]) }, role: "reference_video" });
  }
  for (var a = 0; a < audios.length; a++) {
    content.push({ type: "audio_url", audio_url: { url: refURL(audios[a]) }, role: "reference_audio" });
  }
  return content;
}

function durationOf(input) {
  var duration = Number(input.duration);
  return duration > 0 ? Math.round(duration) : 6;
}

function resolutionOf(input) {
  var raw = textOf(input.resolution).toUpperCase();
  if (raw.indexOf("2K") >= 0) return "2K";
  if (raw.indexOf("768") >= 0) return "768P";
  if (raw.indexOf("480") >= 0) return "480P";
  return "768P";
}

function ratioOf(value, allowAdaptive) {
  var ratio = textOf(value);
  if (ratio && (allowAdaptive || ratio.toLowerCase() !== "adaptive")) return ratio;
  return "16:9";
}

function failed(code, message) {
  var msg = message || "MiniMax 上游任务失败";
  return { status: "failed", error: { class: classifyText(String(code || "") + " " + msg), code: code || undefined, message: msg } };
}

function classifyText(text) {
  var s = String(text || "").toLowerCase();
  if (/1026|1027|sensitive|moderation|content[ _-]?policy|violat|违规|审核|敏感/.test(s)) return "moderation";
  if (/1008|insufficient|balance|quota|credit|欠费|余额|额度/.test(s)) return "provider_balance";
  if (/1002|1039|ratelimit|rate limit|overloaded|too many|限流|过载/.test(s)) return "retryable";
  return "terminal";
}

function refURL(ref) {
  return { __fileRef: ref, as: "url" };
}

function refList(value) {
  return isArray(value) ? value.filter(function (item) { return typeof item === "string" && item !== ""; }) : [];
}

function textOf(value) {
  return typeof value === "string" ? value.replace(/^\s+|\s+$/g, "") : "";
}

function isArray(value) {
  return Object.prototype.toString.call(value) === "[object Array]";
}

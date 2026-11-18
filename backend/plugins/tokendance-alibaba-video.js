/**
 * TokenDance（词元跳动）· 阿里视频插件（内置，契约 v1）。
 *
 * 同一套 DashScope 视频形状承接两个网关协议族：
 *   Wan3.0：POST /alibaba/wan3/v1/video-synthesis，GET /alibaba/wan3/v1/tasks/{id}
 *   HappyHorse：POST /alibaba/happyhorse/v1/video-synthesis（需 X-DashScope-Async: enable），
 *                GET /alibaba/happyhorse/v1/tasks/{id}
 *
 * 请求体是 {model,input:{prompt,media},parameters:{resolution,ratio,duration,...}}；
 * Wan3 支持首尾帧、多模态 reference_* 与视频编辑；HappyHorse 的 r2v 用 reference_image，
 * video-edit 用 type=video。鉴权由宿主注入用户自己的 TokenPay Key。
 */
module.exports = {
  meta: {
    apiVersion: 1,
    key: "tokendance-alibaba-video",
    name: "TokenDance · 阿里视频",
    version: "1.0.0",
    vendor: "alibaba",
    description: "TokenDance Wan3.0 与 HappyHorse 视频生成（异步）",
    auth: { type: "bearer" },
    endpoints: { video: { mode: "async", method: "POST", path: "/alibaba/wan3/v1/video-synthesis" } },
    channelSettings: {
      family: {
        type: "enum",
        label: "协议族",
        options: ["wan3", "happyhorse"],
        default: "wan3",
      },
    },
    poll: { firstDelay: 10, interval: 8, maxInterval: 20, jitter: 0.2 },
  },

  buildSubmitRequest: function (ctx) {
    var family = familyOf(ctx);
    var input = ctx.input || {};
    var parameters = parametersOf(input, family);
    var body = {
      model: ctx.model.upstreamModel,
      input: inputOf(input, family, ctx.model.upstreamModel),
      parameters: parameters,
    };
    var req = { method: "POST", path: pathOf(family, "") + "/video-synthesis", json: body, timeout: 60 };
    if (family === "happyhorse") {
      req.headers = { "X-DashScope-Async": "enable" };
    }
    return req;
  },

  parseSubmitResponse: function (ctx, resp) {
    var body = resp.body || {};
    var out = body.output || {};
    var id = out.task_id || body.task_id;
    if (id === undefined || id === null || id === "") {
      if (body.code || body.message) {
        return { immediate: failed(body.code, body.message) };
      }
      throw new Error("上游没有返回阿里视频任务 ID");
    }
    return { providerTaskId: String(id), state: { family: familyOf(ctx) } };
  },

  buildQueryRequest: function (ctx) {
    var family = familyFromTask(ctx) || familyOf(ctx);
    return {
      method: "GET",
      path: pathOf(family, "") + "/tasks/" + encodeURIComponent(ctx.task.providerTaskId),
      timeout: 30,
    };
  },

  parseQueryResponse: function (ctx, resp) {
    var body = resp.body || {};
    var out = body.output || {};
    var status = String(out.task_status || out.status || "").toUpperCase();
    if (status === "SUCCEEDED" || status === "SUCCESS") {
      var url = out.video_url || out.videoUrl || "";
      if (typeof url !== "string" || url === "") return failed("", "上游返回成功，但没有视频地址");
      return {
        status: "succeeded",
        progress: 100,
        outputs: [{ type: "url", url: url, media_type: "video", mime: "video/mp4" }],
      };
    }
    if (status === "FAILED" || status === "CANCELED" || status === "CANCELLED" || status === "UNKNOWN") {
      return failed(out.code || body.code, out.message || body.message || "上游任务失败：" + status);
    }
    if (status === "RUNNING") return { status: "running", progress: 50 };
    return { status: "queued" };
  },

  classifyError: function (ctx, resp) {
    var body = resp.body && typeof resp.body === "object" ? resp.body : {};
    var message = body.message || (body.error && body.error.message) || (resp.text || "").slice(0, 300);
    var code = body.code || (body.error && body.error.code) || "";
    var cls = classifyText(String(code) + " " + message);
    if (cls === "terminal") return null;
    return { class: cls, code: code ? String(code) : undefined, message: message || undefined };
  },
};

function familyOf(ctx) {
  var settings = (ctx.channel && ctx.channel.settings) || {};
  return settings.family === "happyhorse" ? "happyhorse" : "wan3";
}

function familyFromTask(ctx) {
  var state = ctx.task && ctx.task.state ? ctx.task.state : {};
  return state.family === "happyhorse" || state.family === "wan3" ? state.family : "";
}

function pathOf(family, suffix) {
  return "/alibaba/" + (family === "happyhorse" ? "happyhorse" : "wan3") + "/v1" + suffix;
}

function inputOf(input, family, model) {
  var out = { prompt: textOf(input.prompt) };
  var media = [];
  var images = refList(input.images);
  var videos = refList(input.videos);
  var audios = refList(input.audios);
  if (input.op === "omni") {
    if (family === "happyhorse" && /video-edit/.test(String(model))) {
      if (videos.length > 0) media.push({ type: "video", url: refURL(videos[0]) });
      pushMedia(media, "reference_image", images);
    } else {
      pushMedia(media, "reference_image", images);
      if (family === "wan3") {
        pushMedia(media, "reference_video", videos);
        pushMedia(media, "reference_audio", audios);
      }
    }
  } else if (input.op === "i2v") {
    if (images.length > 0) media.push({ type: "first_frame", url: refURL(images[0]) });
    if (images.length > 1) media.push({ type: "last_frame", url: refURL(images[1]) });
  }
  if (media.length > 0) out.media = media;
  return out;
}

function parametersOf(input, family) {
  var out = {};
  var resolution = textOf(input.resolution).toUpperCase();
  if (resolution) out.resolution = resolution;
  var ratio = textOf(input.aspect_ratio);
  if (ratio && input.op !== "i2v") out.ratio = ratio.toLowerCase() === "auto" ? "adaptive" : ratio;
  var duration = Number(input.duration);
  if (duration > 0) out.duration = Math.round(duration);
  if (family === "wan3") {
    out.prompt_extend = true;
    out.watermark = input.watermark === true;
    if (typeof input.generate_audio === "boolean") out.audio = input.generate_audio;
  }
  return out;
}

function pushMedia(list, type, refs) {
  for (var i = 0; i < refs.length; i++) list.push({ type: type, url: refURL(refs[i]) });
}

function failed(code, message) {
  var msg = message || "阿里视频上游任务失败";
  return { status: "failed", error: { class: classifyText(String(code || "") + " " + msg), code: code || undefined, message: msg } };
}

function classifyText(text) {
  var s = String(text || "").toLowerCase();
  if (/sensitive|moderation|content[ _-]?policy|violat|data[ _-]?inspection|违规|审核|敏感/.test(s)) return "moderation";
  if (/arrearage|insufficient|balance|quota|credit|欠费|余额|额度/.test(s)) return "provider_balance";
  if (/throttling|ratelimit|rate limit|overloaded|too many|限流|过载/.test(s)) return "retryable";
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

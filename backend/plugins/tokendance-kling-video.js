/**
 * TokenDance（词元跳动）· 可灵视频插件（内置，契约 v1）。
 *
 * 网关把可灵原生协议集中在 https://tokendance.space/gateway/kling：
 *   V2（Kling 4.0 / 4.0 Flash）：POST /v2/{text2video,image2video,omni-video}
 *   V1（Kling 3.0 系列）：POST /v1/{text2video,image2video,omni-video,motion-control}
 *   数字人：POST /v1/videos/avatar/image2video
 *   视频特效：POST /v1/videos/effects
 * 查询都在原提交路径后追加任务 ID；鉴权由宿主注入用户自己的 TokenPay Key。
 *
 * 用户输入沿用站内视频节点的统一形状：prompt / op / images / videos / audios，
 * 以及 resolution、duration、aspect_ratio、generate_audio、mode、effect_scene。
 * 图片在文生图路径按关键帧传；omni 路径按 refer_image / feature_video 传。
 */
module.exports = {
  meta: {
    apiVersion: 1,
    key: "tokendance-kling-video",
    name: "TokenDance · 可灵视频",
    version: "1.0.1",
    vendor: "kling",
    description: "TokenDance 可灵视频：Kling 4.0 / 3.0、数字人与视频特效（异步）",
    auth: { type: "bearer" },
    endpoints: { video: { mode: "async", method: "POST", path: "/kling/v2/text2video" } },
    channelSettings: {
      api: {
        type: "enum",
        label: "接口族",
        options: ["v2", "v1", "v1-omni", "avatar", "effects"],
        default: "v2",
      },
    },
    poll: { firstDelay: 15, interval: 10, maxInterval: 25, jitter: 0.2 },
  },

  buildSubmitRequest: function (ctx) {
    var plan = submitPlan(ctx);
    return { method: "POST", path: plan.path, json: plan.body, timeout: 60 };
  },

  parseSubmitResponse: function (ctx, resp) {
    var body = resp.body || {};
    var apiError = apiErrorOf(body);
    if (apiError) {
      return { immediate: failed(apiError.code, apiError.message) };
    }
    var data = dataOf(body);
    var id = data.id || data.task_id;
    if (id === undefined || id === null || id === "") {
      throw new Error("上游没有返回可灵任务 ID");
    }
    return { providerTaskId: String(id), state: { path: submitPlan(ctx).path } };
  },

  buildQueryRequest: function (ctx) {
    var state = ctx.task && ctx.task.state ? ctx.task.state : {};
    var path = typeof state.path === "string" && state.path ? state.path : submitPlan(ctx).path;
    return { method: "GET", path: path + "/" + encodeURIComponent(ctx.task.providerTaskId), timeout: 30 };
  },

  parseQueryResponse: function (ctx, resp) {
    var body = resp.body || {};
    var apiError = apiErrorOf(body);
    if (apiError) {
      return failed(apiError.code, apiError.message);
    }
    var data = dataOf(body);
    var status = String(data.status || data.task_status || data.taskStatus || "").toLowerCase();
    if (status === "succeeded" || status === "succeed" || status === "success") {
      var url = outputURL(data);
      if (!url) {
        return failed("", "上游返回成功，但没有视频地址");
      }
      return {
        status: "succeeded",
        progress: 100,
        outputs: [{ type: "url", url: url, media_type: "video", mime: "video/mp4" }],
      };
    }
    if (status === "failed" || status === "failure" || status === "cancelled" || status === "expired") {
      return failed("", data.task_status_msg || data.message || body.message || "上游任务失败");
    }
    if (status === "processing" || status === "running") {
      return { status: "running", progress: 50 };
    }
    return { status: "queued" };
  },

  classifyError: function (ctx, resp) {
    var body = resp.body && typeof resp.body === "object" ? resp.body : {};
    var message = messageOf(body) || (resp.text || "").slice(0, 300);
    var code = body.code === undefined ? "" : String(body.code);
    var cls = classifyText(code + " " + message);
    if (cls === "terminal") {
      return null;
    }
    return { class: cls, code: code || undefined, message: message || undefined };
  },
};

function submitPlan(ctx) {
  var api = apiOf(ctx);
  if (api === "avatar") {
    return avatarPlan(ctx);
  }
  if (api === "effects") {
    return effectsPlan(ctx);
  }
  var input = ctx.input || {};
  var op = String(input.op || "t2v");
  var images = refList(input.images);
  var videos = refList(input.videos);
  var version = api === "v2" ? "v2" : "v1";
  var base = "/kling/" + version;

  if (op === "omni" && api === "v1" && videos.length > 0) {
    return motionPlan(ctx, base);
  }
  if (api === "v1-omni") {
    return omniPlan(ctx, base);
  }
  if (op === "omni" && (api === "v1-omni" || api === "v2")) {
    return omniPlan(ctx, base);
  }
  if (op === "i2v" || images.length > 0) {
    return imagePlan(ctx, base);
  }
  return textPlan(ctx, base);
}

function apiOf(ctx) {
  var settings = (ctx.channel && ctx.channel.settings) || {};
  var api = String(settings.api || "");
  if (api !== "") {
    return api;
  }
  var model = String((ctx.model && ctx.model.upstreamModel) || "");
  if (model === "kling-avatar-image2video") return "avatar";
  if (model === "kling-effects") return "effects";
  if (model === "kling-3.0-omni") return "v1-omni";
  if (model.indexOf("4.0") >= 0) return "v2";
  return "v1";
}

function textPlan(ctx, base) {
  var input = ctx.input || {};
  var body = { model_name: ctx.model.upstreamModel, settings: settingsOf(input) };
  var prompt = textOf(input.prompt);
  if (prompt) body.prompt = prompt;
  optionsOf(body, input);
  return { path: base + "/text2video", body: body };
}

function imagePlan(ctx, base) {
  var input = ctx.input || {};
  var body = { model_name: ctx.model.upstreamModel, contents: framesOf(input), settings: settingsOf(input) };
  optionsOf(body, input);
  return { path: base + "/image2video", body: body };
}

function omniPlan(ctx, base) {
  var input = ctx.input || {};
  var body = { model_name: ctx.model.upstreamModel, contents: omniContents(input), settings: settingsOf(input) };
  optionsOf(body, input);
  return { path: base + "/omni-video", body: body };
}

function motionPlan(ctx, base) {
  var input = ctx.input || {};
  var images = refList(input.images);
  var videos = refList(input.videos);
  if (images.length === 0 || videos.length === 0) {
    throw new Error("动作控制需要一张人物图片和一段动作参考视频");
  }
  var contents = [];
  if (textOf(input.prompt)) contents.push({ type: "prompt", text: textOf(input.prompt) });
  contents.push({ type: "image", url: imageRef(images[0]) });
  contents.push({ type: "video", url: refURL(videos[0]) });
  var settings = settingsOf(input);
  settings.character_orientation = "video";
  return {
    path: base + "/motion-control",
    body: { model_name: ctx.model.upstreamModel, contents: contents, settings: settings },
  };
}

function avatarPlan(ctx) {
  var input = ctx.input || {};
  var images = refList(input.images);
  var audios = refList(input.audios);
  if (images.length === 0 || audios.length === 0) {
    throw new Error("数字人需要一张人物图片和一段音频");
  }
  var body = {
    model_name: ctx.model.upstreamModel,
    image: imageRef(images[0]),
    sound_file: refURL(audios[0]),
    mode: input.mode === "pro" ? "pro" : "std",
  };
  if (textOf(input.prompt)) body.prompt = textOf(input.prompt);
  return { path: "/kling/v1/videos/avatar/image2video", body: body };
}

function effectsPlan(ctx) {
  var input = ctx.input || {};
  var images = refList(input.images);
  var scene = textOf(input.effect_scene);
  if (!scene) {
    throw new Error("视频特效缺少 effect_scene 模板 ID");
  }
  if (images.length < 1 || images.length > 2) {
    throw new Error("视频特效需要 1 或 2 张图片");
  }
  var visual =
    images.length === 1
      ? { image: imageRef(images[0]) }
      : { images: [imageRef(images[0]), imageRef(images[1])] };
  return {
    path: "/kling/v1/videos/effects",
    body: { model_name: ctx.model.upstreamModel, effect_scene: scene, input: visual },
  };
}

function framesOf(input) {
  var contents = [];
  if (textOf(input.prompt)) contents.push({ type: "prompt", text: textOf(input.prompt) });
  var images = refList(input.images).slice(0, 10);
  for (var i = 0; i < images.length; i++) {
    var type = i === 0 ? "first_frame" : i === images.length - 1 ? "last_frame" : "middle_frame";
    contents.push({ type: type, url: imageRef(images[i]), id: "frame_" + (i + 1) });
  }
  return contents;
}

function omniContents(input) {
  var contents = [];
  if (textOf(input.prompt)) contents.push({ type: "prompt", text: textOf(input.prompt) });
  var images = refList(input.images).slice(0, 10);
  for (var i = 0; i < images.length; i++) {
    contents.push({ type: "refer_image", url: imageRef(images[i]), id: "image_" + (i + 1) });
  }
  var videos = refList(input.videos).slice(0, 5);
  for (var v = 0; v < videos.length; v++) {
    contents.push({ type: "feature_video", url: refURL(videos[v]), id: "video_" + (v + 1) });
  }
  return contents;
}

function settingsOf(input) {
  var settings = {};
  var resolution = textOf(input.resolution).toLowerCase();
  if (resolution) settings.resolution = resolution;
  var duration = Number(input.duration);
  if (duration > 0) settings.duration = Math.round(duration);
  var ratio = textOf(input.aspect_ratio);
  if (ratio) settings.aspect_ratio = ratio.toLowerCase() === "auto" ? "adaptive" : ratio;
  if (typeof input.generate_audio === "boolean") settings.audio = input.generate_audio ? "native" : "off";
  else if (textOf(input.audio)) settings.audio = textOf(input.audio);
  return settings;
}

function optionsOf(body, input) {
  if (input.watermark === true) {
    body.options = { watermark_info: { enabled: true } };
  }
}

function dataOf(body) {
  var data = body.data || {};
  if (isArray(data)) data = data.length > 0 ? data[0] : {};
  return data || {};
}

function outputURL(data) {
  var outputs = isArray(data.outputs) ? data.outputs : [];
  for (var i = 0; i < outputs.length; i++) {
    var item = outputs[i] || {};
    if ((item.type === "video" || !item.type) && typeof item.url === "string" && item.url) return item.url;
  }
  var videos = data.task_result && isArray(data.task_result.videos) ? data.task_result.videos : [];
  return videos.length > 0 && videos[0] && typeof videos[0].url === "string" ? videos[0].url : "";
}

function apiErrorOf(body) {
  if (body && body.code !== undefined && Number(body.code) !== 0) {
    return { code: String(body.code), message: body.message || "可灵上游返回错误" };
  }
  return null;
}

function failed(code, message) {
  var msg = message || "可灵上游任务失败";
  return { status: "failed", error: { class: classifyText(String(code || "") + " " + msg), code: code || undefined, message: msg } };
}

function classifyText(text) {
  var s = String(text || "").toLowerCase();
  if (/sensitive|moderation|content[ _-]?policy|violat|违规|审核|敏感/.test(s)) return "moderation";
  if (/insufficient|balance|arrears|quota|resource pack|余额|欠费|资源包|额度/.test(s)) return "provider_balance";
  if (/ratelimit|rate limit|overloaded|too many|限流|过载|500[0-9]/.test(s)) return "retryable";
  return "terminal";
}

function messageOf(body) {
  if (!body || typeof body !== "object") return "";
  if (typeof body.message === "string") return body.message;
  if (body.error && typeof body.error.message === "string") return body.error.message;
  return "";
}

function refURL(ref) {
  return { __fileRef: ref, as: "url" };
}

// 可灵要求图片宽高都不小于 300px；宿主遇到更小的图会等比放大并以 base64 传入。
function imageRef(ref) {
  return { __fileRef: ref, as: "url", min_side: 300 };
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

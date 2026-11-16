/**
 * TokenDance（词元跳动）· 语音合成插件（内置，契约 v1）。
 * 基于 doubao-audio.js 的同步出音频骨架改写，对接 TokenDance 网关的 MiniMax T2A v2 协议。
 *
 * 接口（渠道 base_url 填 https://tokendance.space/gateway）：
 *   POST /minimax/v1/t2a_v2，同步返回；固定 output_format:"url"（上游返回 24 小时有效的音频下载地址，宿主会转存，
 *   本契约不支持内联 base64 产物，所以不用默认的 hex）。
 * 鉴权：Authorization: Bearer <TokenDance API Key>，由宿主按渠道 Key 注入；钱包渠道注入的是连接钱包的用户自己的 Key。
 * 上游模型（ctx.model.upstreamModel）即 TokenDance 模型目录里的 TTS 模型 ID：minimax-speech-2.8-hd、
 *   minimax-speech-2.8-turbo 等（以 https://tokendance.space/gateway/v1/models 实时目录为准）。
 *
 * 协议设置（模型“协议设置”里填）：
 *   voice_id：音色 ID（MiniMax 系统音色或复刻音色，如 male-qn-qingse），必填；走 MiniMax 文档的音色清单。
 *
 * 写法说明：goja 只保证 ES5.1 + 部分 ES6，所以这里用 var / function，不用可选链与空值合并。
 *
 * 用户输入（宿主按模型能力校验后传入）：
 *   prompt（必填）：待合成文本，支持 MiniMax 的停顿 <#x#> 与发音标注语法；上限交给上游校验；
 *   format（可选）：mp3 / wav / flac，默认 mp3；
 *   speed（数值，可选）：语速 [0.5, 2]，没填就用 1。
 *
 * 刻意没做：stream / 流式分帧、subtitle_enable 字幕、voice 复刻（另有协议）、emotion 等高级 voice_setting 字段。
 * 未验证：错误响应在网关侧的具体形状，按 MiniMax 信封（base_resp.status_code/status_msg）与
 *   OpenAI 风格 error.message 两种都试；余额 / 审核分类只看文案关键字。
 */
module.exports = {
  meta: {
    apiVersion: 1,
    key: "tokendance-tts",
    name: "TokenDance · 语音合成",
    version: "1.0.0",
    vendor: "tokendance",
    description: "TokenDance（词元跳动）MiniMax T2A 语音合成（同步）",
    auth: { type: "bearer" },
    endpoints: {
      audio: { mode: "sync", method: "POST", path: "/minimax/v1/t2a_v2" },
    },
    channelSettings: {
      voice_id: {
        type: "string",
        label: "音色 ID",
        description: "MiniMax 音色 ID（系统音色或复刻音色），如 male-qn-qingse",
        default: "male-qn-qingse",
      },
    },
  },

  buildSubmitRequest: function (ctx) {
    var input = ctx.input || {};
    var prompt = typeof input.prompt === "string" ? input.prompt : "";
    if (prompt.trim() === "") {
      throw new Error("待合成文本 prompt 不能为空");
    }
    var settings = (ctx.channel && ctx.channel.settings) || {};
    var voiceId = typeof settings.voice_id === "string" && settings.voice_id.trim() !== "" ? settings.voice_id.trim() : "male-qn-qingse";

    var body = {
      model: ctx.model.upstreamModel,
      text: prompt,
      stream: false,
      voice_setting: {
        voice_id: voiceId,
        speed: typeof input.speed === "number" ? input.speed : 1,
        vol: 1,
        pitch: 0,
      },
      audio_setting: {
        sample_rate: 32000,
        bitrate: 128000,
        format: formatOf(input),
        channel: 1,
      },
      output_format: "url",
    };
    return { method: "POST", path: "/minimax/v1/t2a_v2", json: body, timeout: 120 };
  },

  parseSubmitResponse: function (ctx, resp) {
    var body = resp.body || {};
    var base = body.base_resp || {};
    if (typeof base.status_code === "number" && base.status_code !== 0) {
      return { immediate: failed(String(base.status_msg || "上游拒绝了请求（" + base.status_code + "）")) };
    }
    var data = body.data || {};
    var url = typeof data.audio === "string" ? data.audio.trim() : "";
    if (url === "") {
      return { immediate: failed("上游返回成功，但没有音频地址") };
    }
    if (!/^https?:/i.test(url)) {
      // 拿到的是内联 hex 而不是 URL：说明上游忽略了 output_format，本契约不支持内联产物
      return { immediate: failed("上游返回了内联音频数据，没有可用地址") };
    }
    return {
      immediate: {
        status: "succeeded",
        progress: 100,
        outputs: [{ type: "url", url: url, media_type: "audio", mime: mimeOf(formatOf(ctx.input || {})) }],
      },
    };
  },

  classifyError: function (ctx, resp) {
    var body = resp.body;
    var msg = "";
    if (body && typeof body === "object") {
      if (body.base_resp && typeof body.base_resp.status_msg === "string" && body.base_resp.status_msg) {
        msg = body.base_resp.status_msg;
      } else if (body.error && typeof body.error.message === "string") {
        msg = body.error.message;
      } else if (typeof body.message === "string") {
        msg = body.message;
      }
    }
    msg = msg || (resp.text || "").slice(0, 300);
    if (!msg) {
      return null;
    }
    var cls = classifyMessage(msg);
    if (cls === "terminal") {
      return null; // 其余交给宿主默认规则（429 / 5xx 重试，其它 terminal）
    }
    return { class: cls, message: msg };
  },
};

function formatOf(input) {
  var format = typeof input.format === "string" ? input.format.toLowerCase() : "";
  if (format === "wav" || format === "flac") {
    return format;
  }
  return "mp3";
}

function mimeOf(format) {
  switch (format) {
    case "wav":
      return "audio/wav";
    case "flac":
      return "audio/flac";
    default:
      return "audio/mpeg";
  }
}

function failed(message) {
  return { status: "failed", error: { class: classifyMessage(message), message: message } };
}

// 按错误文案判断失败分类：审核 → moderation；余额 / 额度 → provider_balance；限流 → retryable；其余 terminal。
function classifyMessage(message) {
  var s = String(message || "").toLowerCase();
  if (/sensitive|nsfw|moderation|content[ _-]?policy|violat|违规|审核|敏感/.test(s)) {
    return "moderation";
  }
  if (/insufficient|balance|quota|credit|overdue|欠费|余额|额度/.test(s)) {
    return "provider_balance";
  }
  if (/ratelimit|rate limit|overloaded|toomanyrequests|限流|过载/.test(s)) {
    return "retryable";
  }
  return "terminal";
}

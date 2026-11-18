// 本文件：生成模型配置自身结构的 JSON Schema（draft-07），给前端 Monaco 编辑器做补全和基础校验。

package modelcfg

import (
	"encoding/json"
	"sync"
)

var (
	schemaOnce  sync.Once
	schemaCache []byte
)

// buildSchema 生成并缓存 JSON Schema 文本。
func buildSchema() {
	b, err := json.MarshalIndent(modelSchema(), "", "  ")
	if err != nil {
		panic("modelcfg: 生成 JSON Schema 失败：" + err.Error())
	}
	schemaCache = b
}

type obj = map[string]any

func str(desc string) obj  { return obj{"type": "string", "description": desc} }
func boolean(d string) obj { return obj{"type": "boolean", "description": d} }
func enum(desc string, values ...string) obj {
	return obj{"type": "string", "enum": values, "description": desc}
}
func duration(desc string) obj {
	return obj{
		"type":        []string{"string", "number"},
		"pattern":     `^[0-9]+(\.[0-9]+)?(ms|s|m|h)$`,
		"description": desc + "，写成 \"10s\" / \"30m\" 这样的字符串",
		"examples":    []string{"10s", "30m"},
	}
}
func object(desc string, props obj, required ...string) obj {
	o := obj{"type": "object", "properties": props, "additionalProperties": false, "description": desc}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

// paramFieldSchema 是 capabilities.params 里单个生成参数的 Schema。
func paramFieldSchema() obj {
	return object("生成参数", obj{
		"type":    enum("参数类型", ParamEnum, ParamNumber, ParamBoolean),
		"label":   str("画布参数面板里的控件标题"),
		"open":    boolean("是否开放给用户：true 出现在画布参数面板，false 不出现、按 default 发送"),
		"options": obj{"type": "array", "maxItems": maxEnumOptions, "description": "enum 的可选值（字符串或数字）", "items": obj{"type": []string{"string", "number"}}},
		"default": obj{"description": "默认值，类型需与参数类型一致；enum 的默认值必须在 options 里"},
		"min":     obj{"type": "integer", "minimum": 1, "maximum": maxNumberValue, "description": "number 的最小值（画布滑块范围）"},
		"max":     obj{"type": "integer", "minimum": 1, "maximum": maxNumberValue, "description": "number 的最大值"},
		"step":    obj{"type": "integer", "minimum": 1, "description": "number 的步长，省略按 1"},
		"unit":    str("展示用的单位，如 秒"),
		"spec":    boolean("可作为规格价格的条件维度（enum / boolean）"),
		"fanout":  boolean("生成数量：一次提交拆成 N 个任务（enum，取值为 1 – 8 的整数，至多一个）"),
	}, "type", "label")
}

func refSpecSchema(desc string) obj {
	return object(desc, obj{
		"on":     boolean("是否接收这种素材"),
		"max":    obj{"type": "integer", "minimum": 0, "maximum": maxRefCount, "description": "最多几个"},
		"max_mb": obj{"type": "integer", "minimum": 0, "maximum": maxRefMB, "description": "单个最大多少 MB"},
	}, "on", "max", "max_mb")
}

// capabilitiesSchema 是 capabilities 的 Schema。
func capabilitiesSchema() obj {
	return object("模型能力：由运营手填，是画布渲染与下单校验的唯一来源", obj{
		"ops": obj{
			"type": "array", "uniqueItems": true, "items": obj{"type": "string", "enum": []string{OpT2V, OpI2V, OpOmni, OpT2I, OpI2I}},
			"description": "生成方式。video：t2v 文生 / i2v 图生 / omni 全能参考；image：t2i 文生图 / i2i 图生图；text、audio、agent 不填",
		},
		"refs": object("参考素材（video / image）", obj{
			"image": refSpecSchema("参考图片"), "audio": refSpecSchema("参考音频"), "video": refSpecSchema("参考视频"),
		}),
		"prompt": object("提示词", obj{
			"max_length": obj{"type": "integer", "minimum": 1, "maximum": maxPromptLength, "description": "提示词字数上限"},
		}, "max_length"),
		"params": obj{
			"type": "object", "additionalProperties": paramFieldSchema(),
			"description": "生成参数：参数名 -> 定义。书写顺序就是画布参数面板的显示顺序；参数名会作为任务输入的键传给插件",
		},
		"context": object("上下文能力（仅 text / agent）", obj{
			"window": obj{"type": "integer", "minimum": 1, "maximum": maxContextWin, "description": "上下文窗口（Token）"},
			"output": obj{"type": "integer", "minimum": minContextOut, "maximum": maxContextOut, "description": "最大输出（Token），小于上下文窗口"},
		}, "window", "output"),
		"system":        str("固定系统提示（仅 text）：每次请求都会带上，用户看不到"),
		"vision":        obj{"type": "boolean", "description": "能不能看图（text / agent）：营销工作流和 Agent 都据此决定是否能输入图片"},
		"video":         obj{"type": "boolean", "description": "能不能理解视频画面和声音（仅 Gemini 格式的 agent）"},
		"prompt_format": obj{"type": "string", "enum": []string{"", PromptFormatSeedance, PromptFormatH3}, "description": "视频模型的提示词写法（仅 video）：短剧分镜据此选模板。留空 = 通用；seedance = 即梦 / Seedance；h3 = MiniMax H3"},
	}, "prompt")
}

// pricingSchema 是历史平台积分字段的兼容占位。新模型不要填写，它不再参与校验、展示或计费。
func pricingSchema() obj {
	return obj{
		"type":                 "object",
		"description":          "历史平台积分字段，仅为旧配置兼容保留；新模型不要填写，也不参与校验、展示或计费",
		"deprecated":           true,
		"additionalProperties": true,
	}
}

// protocolSchema 是 channels[].protocol 的 Schema：模型选的协议插件、固定版本与协议设置项取值。
func protocolSchema() obj {
	return object("调用协议：非 Agent 模型必填，Agent 模型不能有", obj{
		"plugin": obj{
			"type": "string", "pattern": pluginKeyRe.String(),
			"description": "协议插件 key（在插件页上传），小写字母、数字、连字符",
		},
		"version": obj{
			"type": "string", "pattern": semverRe.String(),
			"description": "固定的插件版本（semver）；保存时缺省会补成所选的最新版本",
		},
		"settings": obj{
			"type": "object", "description": "协议设置项取值：按所选版本的 channelSettings 校验，缺省补默认值",
		},
	}, "plugin", "version")
}

// channelRefSchema 是 channels 数组元素的 Schema。
func channelRefSchema() obj {
	return object("模型绑定的一个渠道", obj{
		"channel": obj{
			"type": "string", "pattern": channelKeyRe.String(),
			"description": "渠道 key（在渠道管理里创建），小写字母、数字、连字符",
		},
		"upstream_model": obj{
			"type": "string", "minLength": 1, "maxLength": maxUpstreamModelLen,
			"description": "这个渠道上的上游模型名（工作流型插件里是工作流 id），由模型选的协议插件解释",
		},
		"protocol":   protocolSchema(),
		"api_format": enum("接口格式（仅 agent），不填等于 openai", APIFormatOpenAI, APIFormatGemini),
		"path_prefix": obj{
			"type": "string", "maxLength": maxPathPrefixLen, "pattern": pathPrefixRe.String(),
			"description": "接口路径里的版本段（仅 agent 的 openai 格式），如 /v4；不填等于 /v1，填 / 表示不加前缀",
		},
	}, "channel", "upstream_model")
}

func modelSchema() obj {
	return obj{
		"$schema":              "http://json-schema.org/draft-07/schema#",
		"title":                "Model（模型）",
		"type":                 "object",
		"description":          "画布用户选择的一项模型：绑定哪个渠道与上游模型，以及模型能力；计费统一由用户 TokenPay 钱包按 TokenDance 实际账单结算",
		"additionalProperties": false,
		"required":             []string{"key", "kind", "label", "channels"},
		"properties": obj{
			"key":   obj{"type": "string", "pattern": modelKeyRe.String(), "description": "模型唯一标识"},
			"kind":  enum("模型类型", Kinds...),
			"label": str("下拉里显示的名字"),
			"hint":  obj{"type": "string", "maxLength": maxHintLen, "description": "模型描述：下拉里的小字，可省略，最多 500 字"},
			"vendor": obj{
				"type": "string", "pattern": vendorRe.String(),
				"description": "厂商 slug（如 kling、openai），前端据此显示 logo；可省略，没有对应图标时显示首字头像",
			},
			"tags": obj{
				"type": "array", "maxItems": maxTags, "uniqueItems": true,
				"items":       obj{"type": "string", "minLength": 1, "maxLength": maxTagLen},
				"description": "展示标签，最多 5 个，每个最多 12 字",
			},
			"deadline":     duration("任务整体截止时间，大于 0 且不超过 24h，默认 30m"),
			"idle_timeout": duration("上游响应超时（仅 agent）：上游多久没发数据就判定超时，含等首字节；10s – 10m，不填默认 90s"),
			"enabled":      boolean("是否上架"),
			"sort":         obj{"type": "integer", "description": "排序，升序"},
			"channels": obj{
				"type": "array", "minItems": maxChannels, "maxItems": maxChannels, "items": channelRefSchema(),
				"description": "绑定的渠道。数据结构是数组，预留多渠道故障切换，首期只支持一个渠道",
			},
			"capabilities": capabilitiesSchema(),
			"pricing":      pricingSchema(), // 历史兼容；不要填
		},
	}
}

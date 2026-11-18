package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

// videoCapsSpec 是各类 TokenDance 视频协议生成 capabilities 的共同输入。
type videoCapsSpec struct {
	ops               []string
	imageMax, imageMB int
	videoMax, videoMB int
	audioMax, audioMB int
	promptFormat      string
	params            []string
}

// videoCaps 把视频能力拼成模型正文要求的 JSON；参数顺序就是画布面板顺序。
func videoCaps(spec videoCapsSpec) string {
	format := ""
	if spec.promptFormat != "" {
		format = fmt.Sprintf(`,"prompt_format":%s`, rawJSON(spec.promptFormat))
	}
	return fmt.Sprintf(
		`{"ops":%s,"refs":{"image":{"on":%t,"max":%d,"max_mb":%d},"audio":{"on":%t,"max":%d,"max_mb":%d},"video":{"on":%t,"max":%d,"max_mb":%d}},`+
			`"prompt":{"max_length":20000}%s,"params":{%s}}`,
		rawJSON(spec.ops),
		spec.imageMax > 0, spec.imageMax, spec.imageMB,
		spec.audioMax > 0, spec.audioMax, spec.audioMB,
		spec.videoMax > 0, spec.videoMax, spec.videoMB,
		format, strings.Join(spec.params, ","),
	)
}

func rawJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func enumParam(name, label string, options []string, def string, spec bool) string {
	return fmt.Sprintf(`%q:{"type":"enum","label":%q,"open":true,"options":%s,"default":%q,"spec":%t}`,
		name, label, rawJSON(options), def, spec)
}

func durationParam(minValue, maxValue, def int) string {
	return fmt.Sprintf(`"duration":{"type":"number","label":"视频时长","open":true,"min":%d,"max":%d,"step":1,"default":%d,"unit":"秒"}`, minValue, maxValue, def)
}

func countParam() string {
	return `"count":{"type":"enum","label":"生成数量","open":true,"options":[1,2,4],"default":1,"fanout":true,"unit":"个"}`
}

func audioParam() string {
	return `"generate_audio":{"type":"boolean","label":"生成音频","open":true,"default":true,"spec":true}`
}

const videoRatios = `["Auto","21:9","16:9","4:3","1:1","3:4","9:16","adaptive"]`

func ratioParam(options, def string) string {
	return fmt.Sprintf(`"aspect_ratio":{"type":"enum","label":"比例","open":true,"options":%s,"default":%q}`, options, def)
}

func resolutionParam(options []string, def string) string {
	return enumParam("resolution", "清晰度", options, def, true)
}

func seedanceVideoCaps(entry syncCatalogEntry) string {
	maxDuration := 15
	if strings.Contains(entry.ID, "2.5") {
		maxDuration = 30
	}
	return videoCaps(videoCapsSpec{
		ops: []string{"t2v", "i2v", "omni"}, imageMax: 9, imageMB: 30, videoMax: 3, videoMB: 200, audioMax: 3, audioMB: 15,
		promptFormat: "seedance",
		params: []string{
			ratioParam(videoRatios, "Auto"), resolutionParam([]string{"720P", "1080P"}, "720P"),
			durationParam(4, maxDuration, 5), audioParam(), countParam(),
		},
	})
}

func klingVideoCaps(entry syncCatalogEntry) string {
	switch entry.ID {
	case "kling-4.0":
		return klingGenerationCaps(10, 5, []string{"720P", "1080P"}, 3, 30)
	case "kling-4.0-flash":
		return klingGenerationCaps(10, 5, []string{"720P"}, 3, 20)
	case "kling-3.0":
		return klingGenerationCaps(2, 0, []string{"720P", "1080P", "4K"}, 3, 15)
	case "kling-3.0-turbo":
		return klingGenerationCaps(1, 0, []string{"720P", "1080P"}, 3, 15)
	case "kling-3.0-omni":
		return klingOmniCaps()
	case "kling-avatar-image2video":
		return videoCaps(videoCapsSpec{
			ops: []string{"omni"}, imageMax: 1, imageMB: 30, audioMax: 1, audioMB: 30,
			params: []string{`"mode":{"type":"enum","label":"模式","open":true,"options":["std","pro"],"default":"std"}`},
		})
	case "kling-effects":
		return videoCaps(videoCapsSpec{
			ops: []string{"i2v"}, imageMax: 2, imageMB: 10,
			params: []string{`"effect_scene":{"type":"string","label":"特效模板","open":true,"default":"color_mixing"}`},
		})
	default:
		return klingGenerationCaps(10, 5, []string{"720P", "1080P"}, 3, 30)
	}
}

func klingGenerationCaps(imageMax, videoMax int, resolutions []string, minDuration, maxDuration int) string {
	ops := []string{"t2v", "i2v"}
	if videoMax > 0 {
		ops = append(ops, "omni")
	}
	return videoCaps(videoCapsSpec{
		ops: ops, imageMax: imageMax, imageMB: 30, videoMax: videoMax, videoMB: 200,
		params: []string{
			ratioParam(videoRatios, "16:9"), resolutionParam(resolutions, resolutions[0]),
			durationParam(minDuration, maxDuration, 5), audioParam(),
		},
	})
}

func klingOmniCaps() string {
	return videoCaps(videoCapsSpec{
		ops: []string{"t2v", "omni"}, imageMax: 10, imageMB: 30, videoMax: 1, videoMB: 200,
		params: []string{
			ratioParam(videoRatios, "16:9"), resolutionParam([]string{"720P", "1080P", "4K"}, "720P"),
			durationParam(3, 15, 5), audioParam(),
		},
	})
}

func alibabaVideoCaps(entry syncCatalogEntry) string {
	if strings.HasPrefix(entry.ID, "happyhorse-") {
		return happyHorseVideoCaps(entry)
	}
	return videoCaps(videoCapsSpec{
		ops: []string{"t2v", "i2v", "omni"}, imageMax: 10, imageMB: 30, videoMax: 5, videoMB: 200, audioMax: 5, audioMB: 15,
		params: []string{
			ratioParam(`["adaptive","16:9","4:3","1:1","3:4","9:16"]`, "16:9"),
			resolutionParam([]string{"480P", "720P", "1080P"}, "720P"), durationParam(2, 30, 5), audioParam(),
		},
	})
}

func happyHorseVideoCaps(entry syncCatalogEntry) string {
	base := videoCapsSpec{
		params: []string{
			ratioParam(`["16:9","9:16","1:1","4:3","3:4"]`, "16:9"),
			resolutionParam([]string{"720P", "1080P"}, "720P"), durationParam(3, 15, 5),
		},
	}
	switch {
	case strings.HasSuffix(entry.ID, "-t2v"):
		base.ops = []string{"t2v"}
	case strings.HasSuffix(entry.ID, "-i2v"):
		base.ops, base.imageMax, base.imageMB = []string{"i2v"}, 1, 30
	case strings.HasSuffix(entry.ID, "-r2v"):
		base.ops, base.imageMax, base.imageMB = []string{"omni"}, 9, 30
	case strings.HasSuffix(entry.ID, "-video-edit"):
		base.ops, base.imageMax, base.imageMB, base.videoMax, base.videoMB = []string{"omni"}, 9, 30, 1, 200
		base.params = []string{resolutionParam([]string{"720P", "1080P"}, "720P")}
	default:
		base.ops, base.imageMax, base.imageMB = []string{"omni"}, 9, 30
	}
	return videoCaps(base)
}

func happyHorseTags(entry syncCatalogEntry) []string {
	if strings.HasSuffix(entry.ID, "-video-edit") {
		return []string{"edit"}
	}
	return nil
}

func minimaxVideoCaps(entry syncCatalogEntry) string {
	if entry.ID == "minimax-h3-max" {
		return videoCaps(videoCapsSpec{
			ops: []string{"t2v", "i2v"}, imageMax: 2, imageMB: 30, promptFormat: "h3",
			params: []string{
				ratioParam(`["16:9","4:3","1:1","3:4","9:16","21:9"]`, "16:9"),
				resolutionParam([]string{"480P", "768P"}, "768P"), durationParam(5, 15, 6),
			},
		})
	}
	return videoCaps(videoCapsSpec{
		ops: []string{"t2v", "i2v", "omni"}, imageMax: 9, imageMB: 30, videoMax: 3, videoMB: 200, audioMax: 3, audioMB: 20,
		promptFormat: "h3",
		params: []string{
			ratioParam(`["16:9","4:3","1:1","3:4","9:16","21:9"]`, "16:9"),
			resolutionParam([]string{"768P", "2K"}, "768P"), durationParam(4, 15, 6),
		},
	})
}

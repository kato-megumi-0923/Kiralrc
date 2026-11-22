package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type aiProviderPreset struct {
	Name    string
	BaseURL string
	Models  []string
}

var aiProviderPresets = []aiProviderPreset{
	{Name: "DeepSeek", BaseURL: "https://api.deepseek.com/v1", Models: []string{"deepseek-chat", "deepseek-reasoner"}},
	{Name: "OpenAI", BaseURL: "https://api.openai.com/v1", Models: []string{"gpt-4o-mini", "gpt-4o", "gpt-4.1-mini"}},
	{Name: "Gemini (OpenAI 兼容)", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Models: []string{"gemini-2.5-flash", "gemini-2.5-pro", "gemini-2.0-flash"}},
	{Name: "硅基流动", BaseURL: "https://api.siliconflow.cn/v1", Models: []string{"deepseek-ai/DeepSeek-V3", "Qwen/Qwen2.5-72B-Instruct"}},
	{Name: "智谱 GLM", BaseURL: "https://open.bigmodel.cn/api/paas/v4", Models: []string{"glm-4-flash", "glm-4-plus"}},
	{Name: "通义千问", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", Models: []string{"qwen-plus", "qwen-turbo", "qwen-max"}},
	{Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", Models: []string{"google/gemini-2.5-flash", "deepseek/deepseek-chat", "openai/gpt-4o-mini"}},
	{Name: "本地 Ollama", BaseURL: "http://127.0.0.1:11434/v1", Models: []string{"qwen2.5:14b", "qwen2.5:7b", "gemma2:9b"}},
}

func matchAIProviderPreset(baseURL string) *aiProviderPreset {
	norm := strings.TrimRight(strings.ToLower(strings.TrimSpace(baseURL)), "/")
	if norm == "" {
		return nil
	}
	for i := range aiProviderPresets {
		if strings.TrimRight(strings.ToLower(aiProviderPresets[i].BaseURL), "/") == norm {
			return &aiProviderPresets[i]
		}
	}
	return nil
}

func aiModelSuggestions(baseURL string) []string {
	if p := matchAIProviderPreset(baseURL); p != nil {
		return p.Models
	}
	return []string{
		"deepseek-chat",
		"gpt-4o-mini",
		"gemini-2.5-flash",
		"qwen-plus",
		"glm-4-flash",
	}
}

// maskAPIKey 将 API Key 脱敏为首尾可见的摘要（如 sk-81a94****ac49），避免暴露明文。
func maskAPIKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	rs := []rune(key)
	n := len(rs)
	if n <= 4 {
		return "****"
	}
	if n <= 12 {
		return string(rs[:2]) + "****" + string(rs[n-2:])
	}
	return string(rs[:8]) + "****" + string(rs[n-4:])
}

const aiTranslateSystemPrompt = `你是一位资深音乐歌词翻译，擅长将日语（含 ACG / J-Pop）、英语、韩语等歌词翻译为自然动人的简体中文。
请严格遵守以下规则：
1. **通读全曲再落笔**：结合歌名、歌手风格与整首上下文，准确补全日语常省略的主语（我/你/我们）与语气口吻，副歌或重复出现的句子保持译文一致。
2. **自然现代中文**：贴合原曲情绪与歌词语感，口语自然流畅；严禁生硬机翻腔，也禁止强行古风、文言或过度堆砌辞藻、脑补原词没有的意象。
3. **严格一一对应行数**：输入共 N 行，输出必须严格为 N 条译文。遇到日语跨行倒装句时，在保证中文通顺的前提下尽量贴合每行对应的时间轴语义，绝对禁止合并行或拆分成多行。
4. **特殊内容处理**：若某行含【歌手/合唱】标签则保持语义自然；若某行仅为纯拟声词/无意义哼唱（如 "La la la"、"Ah"）或本身已是中文，可直接原样保留或返回空字符串 ""。
5. **输出格式**：仅输出一个合法的 JSON 字符串数组 ["第1行译文", "第2行译文", ...]，数组长度必须严格等于输入行数 N，不要包含行号、解释说明或多余文字。`

type aiChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type aiChatRequest struct {
	Model       string          `json:"model"`
	Temperature float64         `json:"temperature"`
	Messages    []aiChatMessage `json:"messages"`
}

type aiChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func aiChatCompletionsURL(baseURL string) string {
	u := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if u == "" {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(u), "/chat/completions") {
		return u
	}
	return u + "/chat/completions"
}

func extractSongMeta(metas []string, fallbackFile string) (title, artist string) {
	for _, m := range metas {
		s := strings.TrimSpace(m)
		if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
			continue
		}
		inner := s[1 : len(s)-1]
		idx := strings.IndexByte(inner, ':')
		if idx < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(inner[:idx]))
		val := strings.TrimSpace(inner[idx+1:])
		if val == "" {
			continue
		}
		switch key {
		case "ti":
			if title == "" {
				title = val
			}
		case "ar":
			if artist == "" {
				artist = val
			}
		}
	}
	if title == "" && fallbackFile != "" {
		base := filepath.Base(fallbackFile)
		title = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return title, artist
}

func buildAITranslateUserPrompt(title, artist string, lines []string) string {
	var b strings.Builder
	if title != "" {
		b.WriteString("歌名：")
		b.WriteString(title)
		b.WriteByte('\n')
	}
	if artist != "" {
		b.WriteString("歌手：")
		b.WriteString(artist)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "歌词共 %d 行，请严格输出长度为 %d 的 JSON 字符串数组：\n", len(lines), len(lines))
	for i, ln := range lines {
		fmt.Fprintf(&b, "%d. %s\n", i+1, ln)
	}
	return b.String()
}

func stripMarkdownCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Strip opening ``` or ```json line
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:]
	} else {
		return ""
	}
	if end := strings.LastIndex(s, "```"); end >= 0 {
		s = s[:end]
	}
	return strings.TrimSpace(s)
}

func tryUnmarshalTranslationPayload(raw string) ([]string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	var arr []string
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		return arr, true
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err == nil {
		for _, k := range []string{"translations", "lines", "lyrics", "result", "data"} {
			if v, ok := obj[k]; ok {
				var wrapped []string
				if err := json.Unmarshal(v, &wrapped); err == nil {
					return wrapped, true
				}
			}
		}
	}
	return nil, false
}

// parseAITranslations 解析模型返回的 JSON 译文数组，自动剥离代码块与前后杂散文本，并严格校验长度。
func parseAITranslations(raw string, want int) ([]string, error) {
	cleaned := stripMarkdownCodeFence(raw)
	var arr []string
	var ok bool

	if arr, ok = tryUnmarshalTranslationPayload(cleaned); !ok {
		// 兼容模型在 JSON 前后输出了额外解释文本
		if l, r := strings.IndexByte(cleaned, '['), strings.LastIndexByte(cleaned, ']'); l >= 0 && r > l {
			arr, ok = tryUnmarshalTranslationPayload(cleaned[l : r+1])
		}
	}
	if !ok {
		if l, r := strings.IndexByte(cleaned, '{'), strings.LastIndexByte(cleaned, '}'); l >= 0 && r > l {
			arr, ok = tryUnmarshalTranslationPayload(cleaned[l : r+1])
		}
	}
	if !ok {
		return nil, errors.New("无法解析模型返回的 JSON 译文数组")
	}
	if len(arr) != want {
		return nil, fmt.Errorf("译文行数不匹配：期望 %d 行，模型返回 %d 行", want, len(arr))
	}
	out := make([]string, len(arr))
	for i, s := range arr {
		out[i] = strings.TrimSpace(s)
	}
	return out, nil
}

func requestAITranslations(client *http.Client, baseURL, model, apiKey, title, artist string, lines []string) ([]string, error) {
	endpoint := aiChatCompletionsURL(baseURL)
	if endpoint == "" {
		return nil, errors.New("未配置 Base URL")
	}
	reqBody := aiChatRequest{
		Model:       strings.TrimSpace(model),
		Temperature: 0.3,
		Messages: []aiChatMessage{
			{Role: "system", Content: aiTranslateSystemPrompt},
			{Role: "user", Content: buildAITranslateUserPrompt(title, artist, lines)},
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var parsed aiChatResponse
	_ = json.Unmarshal(body, &parsed)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(parsed.Error.Message))
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return nil, errors.New("模型返回内容为空")
	}
	return parseAITranslations(parsed.Choices[0].Message.Content, len(lines))
}

func (a *app) aiTranslateConfigured() bool {
	baseURL := strings.TrimSpace(a.setAIBaseURL)
	if baseURL == "" || strings.TrimSpace(a.setAIModel) == "" {
		return false
	}
	if strings.TrimSpace(a.setAIAPIKey) != "" {
		return true
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "127.0.0.1" || host == "localhost"
}

func (a *app) editorNeedsTranslation() bool {
	if len(a.edLines) == 0 {
		return false
	}
	var b strings.Builder
	for _, ln := range a.edLines {
		t := strings.TrimSpace(ln.Text)
		if t != "" {
			b.WriteString(t)
			b.WriteByte('\n')
		}
	}
	text := b.String()
	if text == "" {
		return false
	}
	return guessLang(text, "") != "中文"
}

func (a *app) editorHasTranslation() bool {
	for _, ln := range a.edLines {
		if strings.TrimSpace(ln.Translation) != "" {
			return true
		}
	}
	return false
}

func (a *app) translateCurrentLyricsSync(client *http.Client) error {
	if len(a.edLines) == 0 {
		a.toast = "当前没有可翻译的歌词行"
		return errors.New("no lines")
	}
	if !a.aiTranslateConfigured() {
		a.toast = "未接入 AI 翻译 API，请先在「设置」中配置 Base URL、模型和 API Key"
		return errors.New("not configured")
	}
	title, artist := extractSongMeta(a.edMetas, a.edFile)
	origLines := make([]string, len(a.edLines))
	for i, ln := range a.edLines {
		origLines[i] = ln.Text
	}
	trans, err := requestAITranslations(client, a.setAIBaseURL, a.setAIModel, a.setAIAPIKey, title, artist, origLines)
	if err != nil {
		a.toast = "AI 翻译失败：" + err.Error()
		return err
	}
	for i, tr := range trans {
		a.edLines[i].Translation = tr
	}
	a.edModified = true
	a.toast = fmt.Sprintf("AI 翻译完成（共 %d 行），核对无误后点击「保存 LRC」", len(trans))
	return nil
}

func (a *app) translateCurrentLyrics() {
	if a.edTranslating {
		return
	}
	if len(a.edLines) == 0 {
		a.toast = "当前没有可翻译的歌词行"
		return
	}
	if !a.aiTranslateConfigured() {
		a.toast = "未接入 AI 翻译 API，请先在「设置」中配置 Base URL、模型和 API Key"
		return
	}
	baseURL := a.setAIBaseURL
	model := a.setAIModel
	apiKey := a.setAIAPIKey
	snapPath := a.edPath
	title, artist := extractSongMeta(a.edMetas, a.edFile)
	origLines := make([]string, len(a.edLines))
	for i, ln := range a.edLines {
		origLines[i] = ln.Text
	}

	a.edTranslating = true
	a.toast = "正在调用 AI 翻译整首歌词…"
	go func() {
		trans, err := requestAITranslations(nil, baseURL, model, apiKey, title, artist, origLines)
		a.post(func() {
			a.edTranslating = false
			if err != nil {
				a.toast = "AI 翻译失败：" + err.Error()
				return
			}
			if a.edPath != snapPath || len(a.edLines) != len(origLines) {
				a.toast = "已切换歌曲或修改行数，本次 AI 翻译结果未写入"
				return
			}
			for i, tr := range trans {
				a.edLines[i].Translation = tr
			}
			a.edModified = true
			a.toast = fmt.Sprintf("AI 翻译完成（共 %d 行），核对无误后点击「保存 LRC」", len(trans))
		})
	}()
}

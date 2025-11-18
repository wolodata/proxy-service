package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wolodata/proxy-service/internal/ssestream"
)

const (
	perplexityURL = "https://api.perplexity.ai/chat/completions"
)

var apiKey = os.Getenv("PERPLEXITY_API_KEY") // 从环境变量读取

// 测试实际响应结构
func TestActualResponseStructure(t *testing.T) {
	if apiKey == "" {
		t.Skip("跳过：需要设置 PERPLEXITY_API_KEY 环境变量")
	}

	t.Log("测试实际 API 响应结构")

	req := ChatCompletionRequest{
		Model: "sonar",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
		Stream: true,
	}

	reqBody, _ := json.Marshal(req)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", perplexityURL, bytes.NewReader(reqBody))
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	decoder := ssestream.NewDecoder(resp)

	chunkCount := 0
	for decoder.Next() {
		event := decoder.Event()
		chunkCount++

		// 打印前3个响应块的完整 JSON
		if chunkCount <= 3 {
			t.Logf("\n=== 响应块 #%d ===", chunkCount)
			t.Logf("事件类型: %s", event.Type)

			// 格式化 JSON 输出
			var prettyJSON bytes.Buffer
			if err := json.Indent(&prettyJSON, event.Data, "", "  "); err == nil {
				t.Logf("数据:\n%s", prettyJSON.String())
			} else {
				t.Logf("原始数据: %s", string(event.Data))
			}
		}

		if chunkCount >= 3 {
			break
		}
	}

	if err := decoder.Err(); err != nil {
		t.Fatalf("解码错误: %v", err)
	}
}

// 测试 sonar 模型的流式响应
func TestStreamingSonar(t *testing.T) {
	if apiKey == "" {
		t.Skip("跳过：需要设置 PERPLEXITY_API_KEY 环境变量")
	}

	t.Log("开始测试 sonar 模型")

	req := ChatCompletionRequest{
		Model: "sonar",
		Messages: []Message{
			{Role: "user", Content: "What is the capital of France?"},
		},
		Stream: true,
	}

	chunks, err := callPerplexityStream(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("未收到任何响应块")
	}

	t.Logf("收到 %d 个响应块", len(chunks))

	// 拼接完整内容
	var fullContent strings.Builder
	for _, chunk := range chunks {
		fullContent.WriteString(chunk)
	}

	content := fullContent.String()
	t.Logf("完整响应: %s", content)

	if content == "" {
		t.Fatal("响应内容为空")
	}

	// sonar 模型通常不包含 <think> 标签
	if strings.Contains(content, "<think>") {
		t.Log("警告: sonar 模型包含 <think> 标签，这通常不应该发生")
	}
}

// 测试 sonar-deep-research 模型的流式响应（包含 reasoning）
func TestStreamingSonarDeepResearch(t *testing.T) {
	if apiKey == "" {
		t.Skip("跳过：需要设置 PERPLEXITY_API_KEY 环境变量")
	}

	t.Log("开始测试 sonar-deep-research 模型")

	req := ChatCompletionRequest{
		Model: "sonar-deep-research",
		Messages: []Message{
			{Role: "user", Content: "Explain quantum computing in simple terms"},
		},
		Stream: true,
	}

	chunks, err := callPerplexityStream(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("未收到任何响应块")
	}

	t.Logf("收到 %d 个响应块", len(chunks))

	// 拼接完整内容
	var fullContent strings.Builder
	for _, chunk := range chunks {
		fullContent.WriteString(chunk)
	}

	content := fullContent.String()
	t.Logf("完整响应长度: %d", len(content))

	if content == "" {
		t.Fatal("响应内容为空")
	}

	// deep-research 模型可能包含 <think> 标签
	if strings.Contains(content, "<think>") {
		t.Log("检测到 <think> 标签，模型在进行推理")
	}
}

// 测试 <think> 标签解析
func TestThinkTagParsing(t *testing.T) {
	t.Log("测试 <think> 标签解析逻辑")

	testCases := []struct {
		name     string
		input    string
		wantMsg  string
		wantReas string
	}{
		{
			name:     "无标签",
			input:    "Hello world",
			wantMsg:  "Hello world",
			wantReas: "",
		},
		{
			name:     "完整的 think 标签",
			input:    "Before<think>thinking</think>After",
			wantMsg:  "BeforeAfter",
			wantReas: "thinking",
		},
		{
			name:     "多个 think 标签",
			input:    "A<think>T1</think>B<think>T2</think>C",
			wantMsg:  "ABC",
			wantReas: "T1T2",
		},
		{
			name:     "只有 think 标签",
			input:    "<think>only thinking</think>",
			wantMsg:  "",
			wantReas: "only thinking",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var reasoningBuf, messageBuf strings.Builder
			inThinking := false

			for i := 0; i < len(tc.input); {
				if !inThinking && strings.HasPrefix(tc.input[i:], "<think>") {
					inThinking = true
					i += len("<think>")
					continue
				}

				if inThinking && strings.HasPrefix(tc.input[i:], "</think>") {
					inThinking = false
					i += len("</think>")
					continue
				}

				if inThinking {
					reasoningBuf.WriteByte(tc.input[i])
				} else {
					messageBuf.WriteByte(tc.input[i])
				}
				i++
			}

			gotMsg := messageBuf.String()
			gotReas := reasoningBuf.String()

			if gotMsg != tc.wantMsg {
				t.Errorf("消息内容不匹配:\n  期望: %q\n  实际: %q", tc.wantMsg, gotMsg)
			}

			if gotReas != tc.wantReas {
				t.Errorf("推理内容不匹配:\n  期望: %q\n  实际: %q", tc.wantReas, gotReas)
			}
		})
	}
}

// 测试跨 chunk 的标签分割情况
func TestThinkTagSplitAcrossChunks(t *testing.T) {
	t.Log("测试标签跨 chunk 分割的边界情况")

	service := &PerplexityService{}

	testCases := []struct {
		name     string
		chunks   []string // 模拟多个 chunk
		wantMsg  string
		wantReas string
	}{
		{
			name:     "标签在 chunk 边界完整",
			chunks:   []string{"Hello <think>", "thinking", "</think> world"},
			wantMsg:  "Hello  world",
			wantReas: "thinking",
		},
		{
			name:     "开始标签被分割 <thi|nk>",
			chunks:   []string{"Hello <thi", "nk>thinking</think> world"},
			wantMsg:  "Hello  world",
			wantReas: "thinking",
		},
		{
			name:     "结束标签被分割 </thi|nk>",
			chunks:   []string{"Hello <think>thinking</thi", "nk> world"},
			wantMsg:  "Hello  world",
			wantReas: "thinking",
		},
		{
			name:     "标签分割成多个部分 <|th|in|k>",
			chunks:   []string{"Hello <", "th", "in", "k>thinking</", "th", "in", "k> world"},
			wantMsg:  "Hello  world",
			wantReas: "thinking",
		},
		{
			name:     "标签完全在单个 chunk 中",
			chunks:   []string{"Hello <think>thinking</think> world"},
			wantMsg:  "Hello  world",
			wantReas: "thinking",
		},
		{
			name:     "多个 think 标签跨 chunk",
			chunks:   []string{"A<thi", "nk>R1</think>B<think>R", "2</think>C"},
			wantMsg:  "ABC",
			wantReas: "R1R2",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var reasoningBuf, messageBuf strings.Builder
			inThinking := false
			var buffer strings.Builder

			// 模拟流式处理多个 chunk
			for _, deltaContent := range tc.chunks {
				// 将缓冲区内容与当前chunk合并
				if buffer.Len() > 0 {
					deltaContent = buffer.String() + deltaContent
					buffer.Reset()
				}

				i := 0
				for i < len(deltaContent) {
					if !inThinking && strings.HasPrefix(deltaContent[i:], "<think>") {
						inThinking = true
						i += len("<think>")
						continue
					}

					if inThinking && strings.HasPrefix(deltaContent[i:], "</think>") {
						inThinking = false
						i += len("</think>")
						continue
					}

					// 检查是否接近chunk末尾，且可能有部分标签
					remaining := deltaContent[i:]
					if len(remaining) < 8 {
						if service.isPotentialTagPrefix(remaining, inThinking) {
							buffer.WriteString(remaining)
							break
						}
					}

					if inThinking {
						reasoningBuf.WriteByte(deltaContent[i])
					} else {
						messageBuf.WriteByte(deltaContent[i])
					}
					i++
				}
			}

			// 处理缓冲区剩余内容
			if buffer.Len() > 0 {
				remaining := buffer.String()
				if inThinking {
					reasoningBuf.WriteString(remaining)
				} else {
					messageBuf.WriteString(remaining)
				}
			}

			gotMsg := messageBuf.String()
			gotReas := reasoningBuf.String()

			if gotMsg != tc.wantMsg {
				t.Errorf("消息内容不匹配:\n  期望: %q\n  实际: %q", tc.wantMsg, gotMsg)
			}

			if gotReas != tc.wantReas {
				t.Errorf("推理内容不匹配:\n  期望: %q\n  实际: %q", tc.wantReas, gotReas)
			}
		})
	}
}

// 测试温度和 top_p 参数
func TestStreamingWithParameters(t *testing.T) {
	if apiKey == "" {
		t.Skip("跳过：需要设置 PERPLEXITY_API_KEY 环境变量")
	}

	t.Log("测试带参数的流式请求")

	temp := 0.7
	topP := 0.9

	req := ChatCompletionRequest{
		Model: "sonar",
		Messages: []Message{
			{Role: "user", Content: "Say hello"},
		},
		Stream:      true,
		Temperature: &temp,
		TopP:        &topP,
	}

	chunks, err := callPerplexityStream(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("未收到任何响应块")
	}

	t.Logf("成功收到 %d 个响应块", len(chunks))
}

// extractThinkBlocks 提取所有 <think>...</think> 块的内容
func extractThinkBlocks(content string) []string {
	var blocks []string
	for {
		startIdx := strings.Index(content, "<think>")
		if startIdx == -1 {
			break
		}

		endIdx := strings.Index(content[startIdx:], "</think>")
		if endIdx == -1 {
			break
		}

		// 提取 <think> 和 </think> 之间的内容
		thinkContent := content[startIdx+len("<think>") : startIdx+endIdx]
		blocks = append(blocks, thinkContent)

		// 移动到下一个可能的 <think> 标签
		content = content[startIdx+endIdx+len("</think>"):]
	}
	return blocks
}

// removeThinkBlocks 移除所有 <think>...</think> 块
func removeThinkBlocks(content string) string {
	result := content
	for {
		startIdx := strings.Index(result, "<think>")
		if startIdx == -1 {
			break
		}

		endIdx := strings.Index(result[startIdx:], "</think>")
		if endIdx == -1 {
			break
		}

		// 移除整个 <think>...</think> 块
		result = result[:startIdx] + result[startIdx+endIdx+len("</think>"):]
	}
	return result
}

// 辅助函数：调用 Perplexity API 并返回所有响应块
func callPerplexityStream(req ChatCompletionRequest) ([]string, error) {
	// 序列化请求
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	// 为 deep-research 模型设置更长的超时时间
	timeout := 60 * time.Second
	if req.Model == "sonar-deep-research" {
		timeout = 180 * time.Second // 3分钟
	}

	// 创建 HTTP 请求
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", perplexityURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	// 发送请求
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API 返回错误状态码: %d", resp.StatusCode)
	}

	// 使用 ssestream 解析流式响应
	decoder := ssestream.NewDecoder(resp)
	stream := ssestream.NewStream[ChatCompletionChunk](decoder, nil)
	defer stream.Close()

	var chunks []string

	for stream.Next() {
		chunk := stream.Current()

		if len(chunk.Choices) == 0 {
			continue
		}

		deltaContent := chunk.Choices[0].Delta.Content
		if deltaContent != "" {
			chunks = append(chunks, deltaContent)
		}
	}

	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("流处理错误: %w", err)
	}

	return chunks, nil
}

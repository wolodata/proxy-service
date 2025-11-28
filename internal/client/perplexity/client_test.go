package perplexity

import (
	"bufio"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// loadEnvFile 从 .env 文件加载环境变量
func loadEnvFile() (string, error) {
	// 获取项目根目录
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// 向上查找项目根目录（包含 go.mod 的目录）
	dir := wd
	for {
		envPath := filepath.Join(dir, ".env")
		if _, err := os.Stat(envPath); err == nil {
			// 找到 .env 文件
			file, err := os.Open(envPath)
			if err != nil {
				return "", err
			}
			defer file.Close()

			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				// 跳过空行和注释
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}

				// 解析 key=value
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					key := strings.TrimSpace(parts[0])
					value := strings.TrimSpace(parts[1])
					// 移除引号
					value = strings.Trim(value, `"'`)

					if key == "PERPLEXITY_API_KEY" {
						return value, nil
					}
				}
			}
			return "", scanner.Err()
		}

		// 向上一级目录
		parent := filepath.Dir(dir)
		if parent == dir {
			// 已到达根目录
			break
		}
		dir = parent
	}

	return "", nil
}

var apiKey = func() string {
	// 先尝试从 .env 文件读取
	if key, err := loadEnvFile(); err == nil && key != "" {
		return key
	}

	return ""
}()

// TestClient_SonarModel 测试 sonar 模型
func TestClient_SonarModel(t *testing.T) {
	if apiKey == "" {
		t.Skip("跳过：需要在 .env 文件中设置 PERPLEXITY_API_KEY")
	}

	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req := ChatCompletionRequest{
		Model: "sonar",
		Messages: []Message{
			{Role: "user", Content: "中国的首都是哪里"},
		},
		Stream:     true,
		StreamMode: "concise",
	}

	t.Logf("测试模型: %s", req.Model)

	stream, err := client.StreamChatCompletions(ctx, apiKey, req)
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	defer stream.Close()

	chunkCount := 0
	chunkTypes := make(map[string]int)

	for stream.Next() {
		chunk := stream.Current()
		chunkCount++
		chunkTypes[chunk.Object]++

		t.Logf("收到 chunk #%d: type=%s, id=%s", chunkCount, chunk.Object, chunk.ID)
	}

	if err := stream.Err(); err != nil {
		t.Fatalf("流错误: %v", err)
	}

	if chunkCount == 0 {
		t.Fatal("未收到任何 chunk")
	}

	t.Logf("总共收到 %d 个 chunk", chunkCount)
	t.Logf("Chunk 类型分布: %+v", chunkTypes)

	// 验证至少收到了必要的 chunk 类型
	if chunkTypes["chat.completion.chunk"] == 0 {
		t.Error("未收到 chat.completion.chunk")
	}
	if chunkTypes["chat.completion.done"] == 0 {
		t.Error("未收到 chat.completion.done")
	}
}

// TestClient_SonarDeepResearchModel 测试 sonar-deep-research 模型
func TestClient_SonarDeepResearchModel(t *testing.T) {
	if apiKey == "" {
		t.Skip("跳过：需要在 .env 文件中设置 PERPLEXITY_API_KEY")
	}

	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second) // deep-research 需要更长时间
	defer cancel()

	req := ChatCompletionRequest{
		Model: "sonar-deep-research",
		Messages: []Message{
			{Role: "user", Content: "中国的首都是哪里"},
		},
		Stream:     true,
		StreamMode: "concise",
	}

	t.Logf("测试模型: %s", req.Model)

	stream, err := client.StreamChatCompletions(ctx, apiKey, req)
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	defer stream.Close()

	chunkCount := 0
	chunkTypes := make(map[string]int)

	for stream.Next() {
		chunk := stream.Current()
		chunkCount++
		chunkTypes[chunk.Object]++

		t.Logf("收到 chunk #%d: type=%s, id=%s", chunkCount, chunk.Object, chunk.ID)
	}

	if err := stream.Err(); err != nil {
		t.Fatalf("流错误: %v", err)
	}

	if chunkCount == 0 {
		t.Fatal("未收到任何 chunk")
	}

	t.Logf("总共收到 %d 个 chunk", chunkCount)
	t.Logf("Chunk 类型分布: %+v", chunkTypes)

	// 验证至少收到了必要的 chunk 类型
	if chunkTypes["chat.completion.chunk"] == 0 {
		t.Error("未收到 chat.completion.chunk")
	}
	if chunkTypes["chat.completion.done"] == 0 {
		t.Error("未收到 chat.completion.done")
	}
}

// TestClient_ConciseMode_AllChunkTypes 测试简洁模式的所有 chunk 类型
func TestClient_ConciseMode_AllChunkTypes(t *testing.T) {
	if apiKey == "" {
		t.Skip("跳过：需要在 .env 文件中设置 PERPLEXITY_API_KEY")
	}

	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req := ChatCompletionRequest{
		Model: "sonar",
		Messages: []Message{
			{Role: "user", Content: "中国的首都是哪里"},
		},
		Stream:     true,
		StreamMode: "concise",
	}

	stream, err := client.StreamChatCompletions(ctx, apiKey, req)
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	defer stream.Close()

	var (
		hasReasoning      bool
		hasReasoningDone  bool
		hasCompletion     bool
		hasCompletionDone bool
		reasoningSteps    []ReasoningStep
		searchResultCount int
		imageCount        int
		fullContent       string
	)

	for stream.Next() {
		chunk := stream.Current()

		switch chunk.Object {
		case "chat.reasoning":
			hasReasoning = true
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
				reasoningSteps = append(reasoningSteps, chunk.Choices[0].Delta.ReasoningSteps...)
				t.Logf("推理步骤: %d 个新步骤", len(chunk.Choices[0].Delta.ReasoningSteps))
			}

		case "chat.reasoning.done":
			hasReasoningDone = true
			searchResultCount = len(chunk.SearchResults)
			imageCount = len(chunk.Images)
			t.Logf("推理完成: %d 个搜索结果, %d 张图片", searchResultCount, imageCount)

		case "chat.completion.chunk":
			hasCompletion = true
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
				fullContent += chunk.Choices[0].Delta.Content
			}

		case "chat.completion.done":
			hasCompletionDone = true
			if chunk.Usage != nil {
				t.Logf("使用统计: prompt=%d, completion=%d, total=%d",
					chunk.Usage.PromptTokens,
					chunk.Usage.CompletionTokens,
					chunk.Usage.TotalTokens)
				if chunk.Usage.Cost != nil {
					t.Logf("成本: $%.6f", chunk.Usage.Cost.RequestCost)
				}
			}
		}
	}

	if err := stream.Err(); err != nil {
		t.Fatalf("流错误: %v", err)
	}

	// 验证所有必要的 chunk 类型
	if !hasCompletion {
		t.Error("未收到 chat.completion.chunk")
	}
	if !hasCompletionDone {
		t.Error("未收到 chat.completion.done")
	}

	// 简洁模式可能有或没有推理 chunk，这取决于问题类型
	t.Logf("收到的 chunk 类型:")
	t.Logf("  - reasoning: %v", hasReasoning)
	t.Logf("  - reasoning.done: %v", hasReasoningDone)
	t.Logf("  - completion.chunk: %v", hasCompletion)
	t.Logf("  - completion.done: %v", hasCompletionDone)

	if hasReasoning {
		t.Logf("推理步骤总数: %d", len(reasoningSteps))
	}
	if hasReasoningDone {
		t.Logf("搜索结果: %d 个", searchResultCount)
		t.Logf("图片: %d 张", imageCount)
	}

	t.Logf("生成内容长度: %d 字符", len(fullContent))
}

// TestClient_InvalidToken 测试无效 token
func TestClient_InvalidToken(t *testing.T) {
	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := ChatCompletionRequest{
		Model: "sonar",
		Messages: []Message{
			{Role: "user", Content: "中国的首都是哪里"},
		},
		Stream:     true,
		StreamMode: "concise",
	}

	stream, err := client.StreamChatCompletions(ctx, "invalid-token", req)
	if err == nil {
		defer stream.Close()
		// 可能在连接时不报错，但在读取时会报错
		if stream.Next() {
			t.Error("期望失败但成功了")
		}
		if stream.Err() == nil {
			t.Error("期望流错误但没有错误")
		}
	}
	// 如果在连接时就失败了，这也是预期的
}

// TestClient_ContextCancellation 测试上下文取消
func TestClient_ContextCancellation(t *testing.T) {
	if apiKey == "" {
		t.Skip("跳过：需要在 .env 文件中设置 PERPLEXITY_API_KEY")
	}

	client := NewClient()
	ctx, cancel := context.WithCancel(context.Background())

	req := ChatCompletionRequest{
		Model: "sonar",
		Messages: []Message{
			{Role: "user", Content: "中国的首都是哪里"},
		},
		Stream:     true,
		StreamMode: "concise",
	}

	stream, err := client.StreamChatCompletions(ctx, apiKey, req)
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	defer stream.Close()

	chunkCount := 0
	for stream.Next() {
		chunkCount++
		if chunkCount >= 2 {
			cancel() // 收到几个 chunk 后取消
			break
		}
	}

	t.Logf("在取消前收到 %d 个 chunk", chunkCount)
}

// TestClient_WithCustomHTTPClient 测试自定义 HTTP 客户端
func TestClient_WithCustomHTTPClient(t *testing.T) {
	if apiKey == "" {
		t.Skip("跳过：需要在 .env 文件中设置 PERPLEXITY_API_KEY")
	}

	// 创建带自定义超时的 HTTP 客户端
	httpClient := &http.Client{
		Timeout: 60 * time.Second,
	}

	client := NewClientWithHTTPClient(httpClient)
	ctx := context.Background()

	req := ChatCompletionRequest{
		Model: "sonar",
		Messages: []Message{
			{Role: "user", Content: "中国的首都是哪里"},
		},
		Stream:     true,
		StreamMode: "concise",
	}

	stream, err := client.StreamChatCompletions(ctx, apiKey, req)
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	defer stream.Close()

	chunkCount := 0
	for stream.Next() {
		chunkCount++
	}

	if err := stream.Err(); err != nil {
		t.Fatalf("流错误: %v", err)
	}

	if chunkCount == 0 {
		t.Fatal("未收到任何 chunk")
	}

	t.Logf("使用自定义客户端收到 %d 个 chunk", chunkCount)
}

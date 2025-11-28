package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/wolodata/proxy-service/internal/client/perplexity"
	"github.com/wolodata/proxy-service/internal/converter"
	"github.com/wolodata/proxy-service/internal/ssestream"
)

const (
	perplexityURL = "https://api.perplexity.ai/chat/completions"
)

var apiKey = os.Getenv("PERPLEXITY_API_KEY") // 从环境变量读取

// TestConciseModeChunkTypes 测试简洁模式的 4 个 chunk 类型
func TestConciseModeChunkTypes(t *testing.T) {
	if apiKey == "" {
		t.Skip("跳过：需要设置 PERPLEXITY_API_KEY 环境变量")
	}

	t.Log("测试简洁模式的 chunk 类型")

	req := perplexity.ChatCompletionRequest{
		Model: "sonar-pro",
		Messages: []perplexity.Message{
			{Role: "user", Content: "What is artificial intelligence?"},
		},
		Stream:     true,
		StreamMode: "concise",
	}

	chunks, err := callPerplexityConciseStream(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("未收到任何响应块")
	}

	t.Logf("收到 %d 个响应块", len(chunks))

	// 统计各类型 chunk
	chunkTypes := make(map[string]int)
	for _, chunk := range chunks {
		chunkTypes[chunk.Object]++
	}

	t.Logf("Chunk 类型分布: %+v", chunkTypes)

	// 验证至少收到了 completion.chunk 和 completion.done
	if chunkTypes["chat.completion.chunk"] == 0 {
		t.Error("未收到 chat.completion.chunk")
	}
	if chunkTypes["chat.completion.done"] == 0 {
		t.Error("未收到 chat.completion.done")
	}
}

// TestReasoningChunkProcessing 测试推理 chunk 的处理
func TestReasoningChunkProcessing(t *testing.T) {
	service := &PerplexityService{}

	chunk := &perplexity.ConciseChunk{
		ID:      "test-id",
		Object:  "chat.reasoning",
		Model:   "sonar-pro",
		Created: time.Now().Unix(),
		Choices: []perplexity.ConciseChoice{
			{
				Delta: &perplexity.ConciseDelta{
					ReasoningSteps: []perplexity.ReasoningStep{
						{
							Thought: "Let me search for information",
							Type:    "web_search",
						},
					},
				},
			},
		},
	}

	resp, err := service.handleReasoning(chunk)
	if err != nil {
		t.Fatalf("处理失败: %v", err)
	}

	if resp == nil {
		t.Fatal("响应为 nil")
	}

	reasoning := resp.GetReasoning()
	if reasoning == nil {
		t.Fatal("未返回 reasoning chunk")
	}

	if reasoning.Id != "test-id" {
		t.Errorf("ID 不匹配: got %s, want test-id", reasoning.Id)
	}

	if len(reasoning.ReasoningSteps) != 1 {
		t.Errorf("ReasoningSteps 数量不匹配: got %d, want 1", len(reasoning.ReasoningSteps))
	}

	if reasoning.ReasoningSteps[0].Thought != "Let me search for information" {
		t.Errorf("Thought 内容不匹配: got %s", reasoning.ReasoningSteps[0].Thought)
	}
}

// TestReasoningDoneChunkProcessing 测试推理完成 chunk 的处理
func TestReasoningDoneChunkProcessing(t *testing.T) {
	service := &PerplexityService{}

	chunk := &perplexity.ConciseChunk{
		ID:      "test-id",
		Object:  "chat.reasoning.done",
		Model:   "sonar-pro",
		Created: time.Now().Unix(),
		SearchResults: []perplexity.SearchResult{
			{
				Title:   "Test Article",
				URL:     "https://example.com",
				Snippet: "Test snippet",
				Source:  "Example",
			},
		},
		Images: []perplexity.ImageResult{
			{
				URL:   "https://example.com/image.jpg",
				Title: "Test Image",
			},
		},
		Choices: []perplexity.ConciseChoice{
			{
				Message: &perplexity.ConciseMessage{
					ReasoningSteps: []perplexity.ReasoningStep{
						{
							Thought: "Complete reasoning",
							Type:    "summary",
						},
					},
				},
			},
		},
	}

	resp, err := service.handleReasoningDone(chunk)
	if err != nil {
		t.Fatalf("处理失败: %v", err)
	}

	if resp == nil {
		t.Fatal("响应为 nil")
	}

	reasoningDone := resp.GetReasoningDone()
	if reasoningDone == nil {
		t.Fatal("未返回 reasoning_done chunk")
	}

	if len(reasoningDone.SearchResults) != 1 {
		t.Errorf("SearchResults 数量不匹配: got %d, want 1", len(reasoningDone.SearchResults))
	}

	if len(reasoningDone.Images) != 1 {
		t.Errorf("Images 数量不匹配: got %d, want 1", len(reasoningDone.Images))
	}

	if len(reasoningDone.ReasoningSteps) != 1 {
		t.Errorf("ReasoningSteps 数量不匹配: got %d, want 1", len(reasoningDone.ReasoningSteps))
	}
}

// TestCompletionChunkProcessing 测试内容生成 chunk 的处理
func TestCompletionChunkProcessing(t *testing.T) {
	service := &PerplexityService{}

	content := "Hello, this is a test response."
	chunk := &perplexity.ConciseChunk{
		ID:      "test-id",
		Object:  "chat.completion.chunk",
		Model:   "sonar-pro",
		Created: time.Now().Unix(),
		Choices: []perplexity.ConciseChoice{
			{
				Delta: &perplexity.ConciseDelta{
					Content: content,
				},
			},
		},
	}

	resp, err := service.handleCompletionChunk(chunk)
	if err != nil {
		t.Fatalf("处理失败: %v", err)
	}

	if resp == nil {
		t.Fatal("响应为 nil")
	}

	completion := resp.GetCompletion()
	if completion == nil {
		t.Fatal("未返回 completion chunk")
	}

	if completion.Content == nil || *completion.Content != content {
		t.Errorf("Content 不匹配: got %v, want %s", completion.Content, content)
	}
}

// TestCompletionDoneChunkProcessing 测试内容完成 chunk 的处理
func TestCompletionDoneChunkProcessing(t *testing.T) {
	service := &PerplexityService{}

	fullContent := "This is the complete response."
	chunk := &perplexity.ConciseChunk{
		ID:      "test-id",
		Object:  "chat.completion.done",
		Model:   "sonar-pro",
		Created: time.Now().Unix(),
		SearchResults: []perplexity.SearchResult{
			{
				Title:   "Final Article",
				URL:     "https://example.com/final",
				Snippet: "Final snippet",
				Source:  "Example",
			},
		},
		Usage: &perplexity.Usage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
			Cost: &perplexity.Cost{
				InputTokensCost:  0.001,
				OutputTokensCost: 0.002,
				RequestCost:      0.003,
			},
		},
		Choices: []perplexity.ConciseChoice{
			{
				Message: &perplexity.ConciseMessage{
					Content: fullContent,
				},
			},
		},
	}

	resp, err := service.handleCompletionDone(chunk)
	if err != nil {
		t.Fatalf("处理失败: %v", err)
	}

	if resp == nil {
		t.Fatal("响应为 nil")
	}

	completionDone := resp.GetCompletionDone()
	if completionDone == nil {
		t.Fatal("未返回 completion_done chunk")
	}

	if completionDone.Content == nil || *completionDone.Content != fullContent {
		t.Errorf("Content 不匹配: got %v, want %s", completionDone.Content, fullContent)
	}

	if completionDone.Usage == nil {
		t.Fatal("Usage 为 nil")
	}

	if *completionDone.Usage.TotalTokens != 30 {
		t.Errorf("TotalTokens 不匹配: got %d, want 30", *completionDone.Usage.TotalTokens)
	}

	if completionDone.Usage.RequestCost == nil || *completionDone.Usage.RequestCost != 0.003 {
		t.Errorf("RequestCost 不匹配: got %v, want 0.003", completionDone.Usage.RequestCost)
	}

	if len(completionDone.SearchResults) != 1 {
		t.Errorf("SearchResults 数量不匹配: got %d, want 1", len(completionDone.SearchResults))
	}
}

// TestConverterSearchResults 测试搜索结果转换
func TestConverterSearchResults(t *testing.T) {
	input := []perplexity.SearchResult{
		{
			Title:       "Test Article",
			URL:         "https://example.com",
			Date:        "2024-01-01",
			LastUpdated: "2024-01-02",
			Snippet:     "Test snippet",
			Source:      "Example",
		},
	}

	output := converter.ConvertSearchResults(input)

	if len(output) != 1 {
		t.Fatalf("输出长度不匹配: got %d, want 1", len(output))
	}

	if output[0].Title != "Test Article" {
		t.Errorf("Title 不匹配: got %s, want Test Article", output[0].Title)
	}

	if output[0].Date == nil || *output[0].Date != "2024-01-01" {
		t.Errorf("Date 不匹配: got %v, want 2024-01-01", output[0].Date)
	}
}

// TestConverterUsage 测试使用统计转换
func TestConverterUsage(t *testing.T) {
	input := &perplexity.Usage{
		PromptTokens:      100,
		CompletionTokens:  200,
		TotalTokens:       300,
		SearchContextSize: 50,
		Cost: &perplexity.Cost{
			InputTokensCost:  0.01,
			OutputTokensCost: 0.02,
			RequestCost:      0.03,
		},
	}

	output := converter.ConvertUsage(input)

	if output == nil {
		t.Fatal("输出为 nil")
	}

	if *output.PromptTokens != 100 {
		t.Errorf("PromptTokens 不匹配: got %d, want 100", *output.PromptTokens)
	}

	if *output.TotalTokens != 300 {
		t.Errorf("TotalTokens 不匹配: got %d, want 300", *output.TotalTokens)
	}

	if output.RequestCost == nil || *output.RequestCost != 0.03 {
		t.Errorf("RequestCost 不匹配: got %v, want 0.03", output.RequestCost)
	}
}

// TestProcessChunkRouting 测试 chunk 路由
func TestProcessChunkRouting(t *testing.T) {
	service := &PerplexityService{}

	testCases := []struct {
		name       string
		chunkType  string
		wantType   string
		shouldFail bool
	}{
		{
			name:      "reasoning chunk",
			chunkType: "chat.reasoning",
			wantType:  "reasoning",
		},
		{
			name:      "reasoning.done chunk",
			chunkType: "chat.reasoning.done",
			wantType:  "reasoning_done",
		},
		{
			name:      "completion.chunk",
			chunkType: "chat.completion.chunk",
			wantType:  "completion",
		},
		{
			name:      "completion.done",
			chunkType: "chat.completion.done",
			wantType:  "completion_done",
		},
		{
			name:       "unknown type",
			chunkType:  "chat.unknown",
			wantType:   "",
			shouldFail: false, // 返回 nil, 不报错
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			chunk := &perplexity.ConciseChunk{
				ID:      "test-id",
				Object:  tc.chunkType,
				Model:   "sonar-pro",
				Created: time.Now().Unix(),
			}

			// 根据类型添加必要的数据
			switch tc.chunkType {
			case "chat.reasoning":
				chunk.Choices = []perplexity.ConciseChoice{
					{Delta: &perplexity.ConciseDelta{
						ReasoningSteps: []perplexity.ReasoningStep{{Thought: "test"}},
					}},
				}
			case "chat.reasoning.done":
				chunk.Choices = []perplexity.ConciseChoice{
					{Message: &perplexity.ConciseMessage{}},
				}
			case "chat.completion.chunk":
				chunk.Choices = []perplexity.ConciseChoice{
					{Delta: &perplexity.ConciseDelta{Content: "test"}},
				}
			case "chat.completion.done":
				chunk.Choices = []perplexity.ConciseChoice{
					{Message: &perplexity.ConciseMessage{Content: "test"}},
				}
			}

			resp, err := service.processChunk(chunk)

			if tc.shouldFail && err == nil {
				t.Error("期望失败但成功了")
			}
			if !tc.shouldFail && err != nil {
				t.Errorf("不期望失败但失败了: %v", err)
			}

			if tc.wantType != "" && resp == nil {
				t.Error("期望返回响应但得到 nil")
			}

			if tc.wantType == "" && resp != nil {
				t.Error("期望返回 nil 但得到响应")
			}

			// 验证返回的类型
			if resp != nil {
				switch tc.wantType {
				case "reasoning":
					if resp.GetReasoning() == nil {
						t.Error("期望 reasoning chunk")
					}
				case "reasoning_done":
					if resp.GetReasoningDone() == nil {
						t.Error("期望 reasoning_done chunk")
					}
				case "completion":
					if resp.GetCompletion() == nil {
						t.Error("期望 completion chunk")
					}
				case "completion_done":
					if resp.GetCompletionDone() == nil {
						t.Error("期望 completion_done chunk")
					}
				}
			}
		})
	}
}

// TestActualConciseMode 测试实际的简洁模式 API 响应
func TestActualConciseMode(t *testing.T) {
	if apiKey == "" {
		t.Skip("跳过：需要设置 PERPLEXITY_API_KEY 环境变量")
	}

	t.Log("测试实际简洁模式 API")

	req := perplexity.ChatCompletionRequest{
		Model: "sonar-pro",
		Messages: []perplexity.Message{
			{Role: "user", Content: "What is the capital of France?"},
		},
		Stream:     true,
		StreamMode: "concise",
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
	stream := ssestream.NewStream[perplexity.ConciseChunk](decoder, nil)
	defer stream.Close()

	chunkCount := 0
	var hasCompletion, hasCompletionDone bool

	for stream.Next() {
		chunk := stream.Current()
		chunkCount++

		t.Logf("收到 chunk #%d, type: %s, id: %s", chunkCount, chunk.Object, chunk.ID)

		switch chunk.Object {
		case "chat.reasoning":
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
				t.Logf("  推理步骤数: %d", len(chunk.Choices[0].Delta.ReasoningSteps))
			}
		case "chat.reasoning.done":
			t.Logf("  搜索结果数: %d", len(chunk.SearchResults))
			t.Logf("  图片数: %d", len(chunk.Images))
		case "chat.completion.chunk":
			hasCompletion = true
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
				t.Logf("  内容长度: %d", len(chunk.Choices[0].Delta.Content))
			}
		case "chat.completion.done":
			hasCompletionDone = true
			if chunk.Usage != nil {
				t.Logf("  Token 使用: %d (prompt) + %d (completion) = %d (total)",
					chunk.Usage.PromptTokens,
					chunk.Usage.CompletionTokens,
					chunk.Usage.TotalTokens)
				if chunk.Usage.Cost != nil {
					t.Logf("  成本: $%.6f", chunk.Usage.Cost.RequestCost)
				}
			}
		}

		if chunkCount >= 10 {
			break
		}
	}

	if err := stream.Err(); err != nil {
		t.Fatalf("流错误: %v", err)
	}

	if !hasCompletion {
		t.Error("未收到 chat.completion.chunk")
	}
	if !hasCompletionDone {
		t.Error("未收到 chat.completion.done")
	}

	t.Logf("总共收到 %d 个 chunk", chunkCount)
}

// 辅助函数：调用 Perplexity 简洁模式 API
func callPerplexityConciseStream(req perplexity.ChatCompletionRequest) ([]perplexity.ConciseChunk, error) {
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", perplexityURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API 返回错误状态码: %d", resp.StatusCode)
	}

	decoder := ssestream.NewDecoder(resp)
	stream := ssestream.NewStream[perplexity.ConciseChunk](decoder, nil)
	defer stream.Close()

	var chunks []perplexity.ConciseChunk

	for stream.Next() {
		chunk := stream.Current()
		chunks = append(chunks, chunk)
	}

	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("流处理错误: %w", err)
	}

	return chunks, nil
}

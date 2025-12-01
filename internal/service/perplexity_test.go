package service

import (
	"os"
	"strings"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
	pbv1 "github.com/wolodata/proxy-service/api/proxy/v1"
)

func TestExtractThinkTags(t *testing.T) {
	logger := log.NewStdLogger(os.Stdout)
	service := NewPerplexityService(logger)

	tests := []struct {
		name            string
		content         string
		expectResponses int
		expectReasoning bool
		expectCompletion bool
	}{
		{
			name:             "没有 think 标签",
			content:          "普通的文本内容",
			expectResponses:  1,
			expectReasoning:  false,
			expectCompletion: true,
		},
		{
			name:             "完整的 think 标签",
			content:          "<think>这是推理内容</think>",
			expectResponses:  1,
			expectReasoning:  true,
			expectCompletion: false,
		},
		{
			name:             "think 标签前有内容",
			content:          "前面的内容<think>推理内容</think>",
			expectResponses:  2,
			expectReasoning:  true,
			expectCompletion: true,
		},
		{
			name:             "think 标签后有内容",
			content:          "<think>推理内容</think>后面的内容",
			expectResponses:  2,
			expectReasoning:  true,
			expectCompletion: true,
		},
		{
			name:             "think 标签前后都有内容",
			content:          "前面<think>推理</think>后面",
			expectResponses:  3,
			expectReasoning:  true,
			expectCompletion: true,
		},
		{
			name:             "多个 think 标签",
			content:          "<think>第一个</think>中间<think>第二个</think>",
			expectResponses:  3,
			expectReasoning:  true,
			expectCompletion: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var inThinkTag bool
			var thinkContent strings.Builder

			responses := service.extractThinkTags(
				tt.content,
				&inThinkTag,
				&thinkContent,
				"test-id",
				"test-model",
				1234567890,
			)

			if len(responses) != tt.expectResponses {
				t.Errorf("期望 %d 个响应，实际得到 %d 个", tt.expectResponses, len(responses))
			}

			hasReasoning := false
			hasCompletion := false

			for _, resp := range responses {
				switch resp.Data.(type) {
				case *pbv1.StreamChatCompletionsResponse_Reasoning:
					hasReasoning = true
				case *pbv1.StreamChatCompletionsResponse_Completion:
					hasCompletion = true
				}
			}

			if hasReasoning != tt.expectReasoning {
				t.Errorf("期望 reasoning=%v，实际得到 %v", tt.expectReasoning, hasReasoning)
			}

			if hasCompletion != tt.expectCompletion {
				t.Errorf("期望 completion=%v，实际得到 %v", tt.expectCompletion, hasCompletion)
			}
		})
	}
}

func TestExtractThinkTags_Streaming(t *testing.T) {
	logger := log.NewStdLogger(os.Stdout)
	service := NewPerplexityService(logger)

	// 模拟流式接收，think 标签跨越多个 chunk
	var inThinkTag bool
	var thinkContent strings.Builder

	// Chunk 1: <think> 开始
	responses1 := service.extractThinkTags(
		"<think>\n",
		&inThinkTag,
		&thinkContent,
		"test-id",
		"test-model",
		1234567890,
	)

	if len(responses1) != 0 {
		t.Errorf("Chunk 1: 期望 0 个响应，实际得到 %d 个", len(responses1))
	}

	if !inThinkTag {
		t.Error("Chunk 1: 应该进入 think 标签状态")
	}

	// Chunk 2: think 内容
	responses2 := service.extractThinkTags(
		"这是推理的",
		&inThinkTag,
		&thinkContent,
		"test-id",
		"test-model",
		1234567890,
	)

	if len(responses2) != 0 {
		t.Errorf("Chunk 2: 期望 0 个响应，实际得到 %d 个", len(responses2))
	}

	if !inThinkTag {
		t.Error("Chunk 2: 应该仍在 think 标签状态")
	}

	// Chunk 3: think 内容继续
	responses3 := service.extractThinkTags(
		"内容。",
		&inThinkTag,
		&thinkContent,
		"test-id",
		"test-model",
		1234567890,
	)

	if len(responses3) != 0 {
		t.Errorf("Chunk 3: 期望 0 个响应，实际得到 %d 个", len(responses3))
	}

	if !inThinkTag {
		t.Error("Chunk 3: 应该仍在 think 标签状态")
	}

	// Chunk 4: </think> 结束并有后续内容
	responses4 := service.extractThinkTags(
		"\n</think>\n\n",
		&inThinkTag,
		&thinkContent,
		"test-id",
		"test-model",
		1234567890,
	)

	if len(responses4) != 1 {
		t.Fatalf("Chunk 4: 期望 1 个响应，实际得到 %d 个", len(responses4))
	}

	if inThinkTag {
		t.Error("Chunk 4: 应该退出 think 标签状态")
	}

	// 验证是 reasoning 响应
	if _, ok := responses4[0].Data.(*pbv1.StreamChatCompletionsResponse_Reasoning); !ok {
		t.Error("Chunk 4: 期望得到 reasoning 响应")
	}

	// Chunk 5: 正常内容
	responses5 := service.extractThinkTags(
		"这是正常的回复内容",
		&inThinkTag,
		&thinkContent,
		"test-id",
		"test-model",
		1234567890,
	)

	if len(responses5) != 1 {
		t.Fatalf("Chunk 5: 期望 1 个响应，实际得到 %d 个", len(responses5))
	}

	if _, ok := responses5[0].Data.(*pbv1.StreamChatCompletionsResponse_Completion); !ok {
		t.Error("Chunk 5: 期望得到 completion 响应")
	}
}

func TestExtractPartialTag(t *testing.T) {
	logger := log.NewStdLogger(os.Stdout)
	service := NewPerplexityService(logger)

	tests := []struct {
		name       string
		content    string
		inThinkTag bool
		expected   string
	}{
		// 不在 think 标签内的测试
		{
			name:       "不在标签内 - 没有部分标签",
			content:    "普通文本",
			inThinkTag: false,
			expected:   "",
		},
		{
			name:       "不在标签内 - 单个 <",
			content:    "文本<",
			inThinkTag: false,
			expected:   "<",
		},
		{
			name:       "不在标签内 - <t",
			content:    "文本<t",
			inThinkTag: false,
			expected:   "<t",
		},
		{
			name:       "不在标签内 - <th",
			content:    "文本<th",
			inThinkTag: false,
			expected:   "<th",
		},
		{
			name:       "不在标签内 - <thi",
			content:    "文本<thi",
			inThinkTag: false,
			expected:   "<thi",
		},
		{
			name:       "不在标签内 - <thin",
			content:    "文本<thin",
			inThinkTag: false,
			expected:   "<thin",
		},
		{
			name:       "不在标签内 - <think",
			content:    "文本<think",
			inThinkTag: false,
			expected:   "<think",
		},
		{
			name:       "不在标签内 - 完整标签不是部分标签",
			content:    "文本<think>",
			inThinkTag: false,
			expected:   "",
		},

		// 在 think 标签内的测试
		{
			name:       "在标签内 - 没有部分标签",
			content:    "推理内容",
			inThinkTag: true,
			expected:   "",
		},
		{
			name:       "在标签内 - 单个 <",
			content:    "推理内容<",
			inThinkTag: true,
			expected:   "<",
		},
		{
			name:       "在标签内 - </",
			content:    "推理内容</",
			inThinkTag: true,
			expected:   "</",
		},
		{
			name:       "在标签内 - </t",
			content:    "推理内容</t",
			inThinkTag: true,
			expected:   "</t",
		},
		{
			name:       "在标签内 - </th",
			content:    "推理内容</th",
			inThinkTag: true,
			expected:   "</th",
		},
		{
			name:       "在标签内 - </thi",
			content:    "推理内容</thi",
			inThinkTag: true,
			expected:   "</thi",
		},
		{
			name:       "在标签内 - </thin",
			content:    "推理内容</thin",
			inThinkTag: true,
			expected:   "</thin",
		},
		{
			name:       "在标签内 - </think",
			content:    "推理内容</think",
			inThinkTag: true,
			expected:   "</think",
		},
		{
			name:       "在标签内 - 完整标签不是部分标签",
			content:    "推理内容</think>",
			inThinkTag: true,
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.extractPartialTag(tt.content, tt.inThinkTag)
			if result != tt.expected {
				t.Errorf("期望 %q，实际得到 %q", tt.expected, result)
			}
		})
	}
}

func TestExtractThinkTags_PartialTagHandling(t *testing.T) {
	logger := log.NewStdLogger(os.Stdout)
	service := NewPerplexityService(logger)

	tests := []struct {
		name            string
		chunks          []string
		expectReasoning bool
		expectContent   string
	}{
		{
			name: "开始标签被分割 - <thi|nk>",
			chunks: []string{
				"一些文本<thi",
				"nk>推理内容</think>后续内容",
			},
			expectReasoning: true,
			expectContent:   "推理内容",
		},
		{
			name: "结束标签被分割 - </thi|nk>",
			chunks: []string{
				"<think>推理内容</thi",
				"nk>后续内容",
			},
			expectReasoning: true,
			expectContent:   "推理内容",
		},
		{
			name: "两个标签都被分割",
			chunks: []string{
				"文本<th",
				"ink>推理</th",
				"ink>更多文本",
			},
			expectReasoning: true,
			expectContent:   "推理",
		},
		{
			name: "单字符分割 - <|think>",
			chunks: []string{
				"文本<",
				"think>推理</think>",
			},
			expectReasoning: true,
			expectContent:   "推理",
		},
		{
			name: "单字符分割 - <think|>",
			chunks: []string{
				"<think",
				">推理</think>",
			},
			expectReasoning: true,
			expectContent:   "推理",
		},
		{
			name: "多次分割",
			chunks: []string{
				"<",
				"t",
				"h",
				"i",
				"n",
				"k",
				">",
				"推理",
				"<",
				"/",
				"t",
				"h",
				"i",
				"n",
				"k",
				">",
			},
			expectReasoning: true,
			expectContent:   "推理",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				inThinkTag   bool
				thinkContent strings.Builder
				partialTag   string
				allResponses []*pbv1.StreamChatCompletionsResponse
			)

			// 模拟流式处理
			for _, chunk := range tt.chunks {
				// 合并上次保存的部分标签
				if partialTag != "" {
					chunk = partialTag + chunk
					partialTag = ""
				}

				// 检查内容结尾是否可能是被截断的标签
				newPartialTag := service.extractPartialTag(chunk, inThinkTag)
				if newPartialTag != "" {
					partialTag = newPartialTag
					chunk = chunk[:len(chunk)-len(newPartialTag)]
				}

				// 如果移除部分标签后内容为空，继续下一个 chunk
				if chunk == "" {
					continue
				}

				responses := service.extractThinkTags(
					chunk,
					&inThinkTag,
					&thinkContent,
					"test-id",
					"test-model",
					1234567890,
				)
				allResponses = append(allResponses, responses...)
			}

			// 验证结果
			hasReasoning := false
			var reasoningContent string

			for _, resp := range allResponses {
				if reasoning, ok := resp.Data.(*pbv1.StreamChatCompletionsResponse_Reasoning); ok {
					hasReasoning = true
					if len(reasoning.Reasoning.ReasoningSteps) > 0 {
						reasoningContent = reasoning.Reasoning.ReasoningSteps[0].Thought
					}
				}
			}

			if hasReasoning != tt.expectReasoning {
				t.Errorf("期望 reasoning=%v，实际得到 %v", tt.expectReasoning, hasReasoning)
			}

			if tt.expectReasoning && reasoningContent != tt.expectContent {
				t.Errorf("期望推理内容 %q，实际得到 %q", tt.expectContent, reasoningContent)
			}
		})
	}
}

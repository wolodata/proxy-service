package service

import (
	"strings"

	"github.com/go-kratos/kratos/v2/log"

	pbv1 "github.com/wolodata/proxy-service/api/proxy/v1"
	"github.com/wolodata/proxy-service/internal/client/perplexity"
	"github.com/wolodata/proxy-service/internal/converter"
)

type PerplexityService struct {
	pbv1.UnimplementedPerplexityServer
	log    *log.Helper
	client *perplexity.Client
}

func NewPerplexityService(logger log.Logger) *PerplexityService {
	return &PerplexityService{
		log:    log.NewHelper(logger),
		client: perplexity.NewClient(),
	}
}

// extractPartialTag 检查内容结尾是否可能是被截断的标签
// 返回可能的部分标签，如果没有则返回空字符串
func (s *PerplexityService) extractPartialTag(content string, inThinkTag bool) string {
	if len(content) == 0 {
		return ""
	}

	// 定义可能的标签前缀（从长到短）
	openTagPrefixes := []string{"<think", "<thin", "<thi", "<th", "<t", "<"}
	closeTagPrefixes := []string{"</think", "</thin", "</thi", "</th", "</t", "</", "<"}

	var prefixesToCheck []string

	if inThinkTag {
		// 在 think 标签内，优先检查结束标签
		prefixesToCheck = closeTagPrefixes
	} else {
		// 不在 think 标签内，需要检查两种情况：
		// 1. 如果内容中包含 <think>，可能正在进入标签，所以也要检查结束标签前缀
		// 2. 否则只检查开始标签前缀
		if strings.Contains(content, "<think>") {
			// 内容中已经包含了开始标签，检查结束标签前缀
			prefixesToCheck = closeTagPrefixes
		} else {
			// 只检查开始标签前缀
			prefixesToCheck = openTagPrefixes
		}
	}

	// 从最长的可能标签开始检查
	for _, prefix := range prefixesToCheck {
		// 检查内容是否以这个部分标签结尾
		if strings.HasSuffix(content, prefix) {
			return prefix
		}
	}

	return ""
}

// extractThinkTags 从内容中提取 <think> 标签，并返回相应的响应列表
func (s *PerplexityService) extractThinkTags(
	content string,
	inThinkTag *bool,
	thinkContent *strings.Builder,
	chunkID string,
	model string,
	created int64,
) []*pbv1.StreamChatCompletionsResponse {
	var responses []*pbv1.StreamChatCompletionsResponse

	for len(content) > 0 {
		if !*inThinkTag {
			// 当前不在 think 标签内，查找 <think> 开始标签
			thinkStart := strings.Index(content, "<think>")
			if thinkStart == -1 {
				// 没有找到 <think> 标签，整个内容都是普通内容
				// 过滤掉纯空白内容
				if content != "" && strings.TrimSpace(content) != "" {
					responses = append(responses, &pbv1.StreamChatCompletionsResponse{
						Data: &pbv1.StreamChatCompletionsResponse_Completion{
							Completion: &pbv1.CompletionChunk{
								Id:      chunkID,
								Model:   model,
								Created: created,
								Content: &content,
							},
						},
					})
				}
				break
			}

			// 找到 <think> 标签，先发送标签前的内容
			if thinkStart > 0 {
				beforeThink := content[:thinkStart]
				// 过滤掉纯空白内容
				if strings.TrimSpace(beforeThink) != "" {
					responses = append(responses, &pbv1.StreamChatCompletionsResponse{
						Data: &pbv1.StreamChatCompletionsResponse_Completion{
							Completion: &pbv1.CompletionChunk{
								Id:      chunkID,
								Model:   model,
								Created: created,
								Content: &beforeThink,
							},
						},
					})
				}
			}

			// 进入 think 标签
			*inThinkTag = true
			thinkContent.Reset()
			content = content[thinkStart+7:] // 跳过 "<think>"
			s.log.Debugw("msg", "进入 think 标签")
		} else {
			// 当前在 think 标签内，查找 </think> 结束标签
			thinkEnd := strings.Index(content, "</think>")
			if thinkEnd == -1 {
				// 没有找到结束标签，整个内容都是 think 内容
				thinkContent.WriteString(content)
				break
			}

			// 找到 </think> 标签，提取 think 内容
			thinkContent.WriteString(content[:thinkEnd])

			// 发送 reasoning chunk
			thinkText := strings.TrimSpace(thinkContent.String())
			if thinkText != "" {
				s.log.Debugw("msg", "发送 reasoning chunk", "length", len(thinkText))
				responses = append(responses, &pbv1.StreamChatCompletionsResponse{
					Data: &pbv1.StreamChatCompletionsResponse_Reasoning{
						Reasoning: &pbv1.ReasoningChunk{
							Id:      chunkID,
							Model:   model,
							Created: created,
							ReasoningSteps: []*pbv1.ReasoningStep{
								{
									Thought: thinkText,
									Type:    "thinking",
								},
							},
						},
					},
				})
			}

			// 退出 think 标签
			*inThinkTag = false
			content = content[thinkEnd+8:] // 跳过 "</think>"
			s.log.Debugw("msg", "退出 think 标签")
		}
	}

	return responses
}

// 处理不同类型的 chunk
func (s *PerplexityService) processChunk(chunk *perplexity.ConciseChunk) (*pbv1.StreamChatCompletionsResponse, error) {
	switch chunk.Object {
	case "chat.reasoning":
		return s.handleReasoning(chunk)
	case "chat.reasoning.done":
		return s.handleReasoningDone(chunk)
	case "chat.completion.chunk":
		return s.handleCompletionChunk(chunk)
	case "chat.completion.done":
		return s.handleCompletionDone(chunk)
	default:
		s.log.Warnw("msg", "未知的 chunk 类型", "type", chunk.Object)
		return nil, nil
	}
}

func (s *PerplexityService) handleReasoning(chunk *perplexity.ConciseChunk) (*pbv1.StreamChatCompletionsResponse, error) {
	if len(chunk.Choices) == 0 || chunk.Choices[0].Delta == nil {
		return nil, nil
	}

	delta := chunk.Choices[0].Delta
	if len(delta.ReasoningSteps) == 0 {
		return nil, nil
	}

	return &pbv1.StreamChatCompletionsResponse{
		Data: &pbv1.StreamChatCompletionsResponse_Reasoning{
			Reasoning: &pbv1.ReasoningChunk{
				Id:             chunk.ID,
				Model:          chunk.Model,
				Created:        chunk.Created,
				ReasoningSteps: converter.ConvertReasoningSteps(delta.ReasoningSteps),
			},
		},
	}, nil
}

func (s *PerplexityService) handleReasoningDone(chunk *perplexity.ConciseChunk) (*pbv1.StreamChatCompletionsResponse, error) {
	reasoningDone := &pbv1.ReasoningDoneChunk{
		Id:            chunk.ID,
		Model:         chunk.Model,
		Created:       chunk.Created,
		SearchResults: converter.ConvertSearchResults(chunk.SearchResults),
		Images:        converter.ConvertImageResults(chunk.Images),
	}

	// 从 message 获取完整的 reasoning steps
	if len(chunk.Choices) > 0 && chunk.Choices[0].Message != nil {
		reasoningDone.ReasoningSteps = converter.ConvertReasoningSteps(chunk.Choices[0].Message.ReasoningSteps)
	}

	return &pbv1.StreamChatCompletionsResponse{
		Data: &pbv1.StreamChatCompletionsResponse_ReasoningDone{
			ReasoningDone: reasoningDone,
		},
	}, nil
}

func (s *PerplexityService) handleCompletionChunk(chunk *perplexity.ConciseChunk) (*pbv1.StreamChatCompletionsResponse, error) {
	if len(chunk.Choices) == 0 || chunk.Choices[0].Delta == nil {
		return nil, nil
	}

	delta := chunk.Choices[0].Delta
	if delta.Content == "" {
		return nil, nil
	}

	return &pbv1.StreamChatCompletionsResponse{
		Data: &pbv1.StreamChatCompletionsResponse_Completion{
			Completion: &pbv1.CompletionChunk{
				Id:      chunk.ID,
				Model:   chunk.Model,
				Created: chunk.Created,
				Content: &delta.Content,
			},
		},
	}, nil
}

func (s *PerplexityService) handleCompletionDone(chunk *perplexity.ConciseChunk) (*pbv1.StreamChatCompletionsResponse, error) {
	completionDone := &pbv1.CompletionDoneChunk{
		Id:      chunk.ID,
		Model:   chunk.Model,
		Created: chunk.Created,
		Usage:   converter.ConvertUsage(chunk.Usage),
	}

	// 从 message 获取完整内容
	if len(chunk.Choices) > 0 && chunk.Choices[0].Message != nil {
		completionDone.Content = &chunk.Choices[0].Message.Content
	}

	return &pbv1.StreamChatCompletionsResponse{
		Data: &pbv1.StreamChatCompletionsResponse_CompletionDone{
			CompletionDone: completionDone,
		},
	}, nil
}

func (s *PerplexityService) StreamChatCompletions(req *pbv1.StreamChatCompletionsRequest, conn pbv1.Perplexity_StreamChatCompletionsServer) error {
	s.log.Infow(
		"msg", "流式对话补全开始（简洁模式）",
		"model", req.GetModel(),
		"message_count", len(req.GetMessages()),
		"temperature", req.GetTemperature(),
		"top_p", req.GetTopP(),
	)

	// 1. 验证基本参数
	token := strings.TrimSpace(req.GetToken())
	if token == "" {
		return pbv1.ErrorInvalidArgument("token 为空")
	}

	// 2. 转换消息格式
	msgs := make([]perplexity.Message, 0, len(req.GetMessages()))
	for _, msg := range req.GetMessages() {
		var role string
		switch msg.GetRole() {
		case pbv1.MessageRole_CHAT_COMPLETION_MESSAGE_ROLE_SYSTEM:
			role = "system"
		case pbv1.MessageRole_CHAT_COMPLETION_MESSAGE_ROLE_USER:
			role = "user"
		case pbv1.MessageRole_CHAT_COMPLETION_MESSAGE_ROLE_ASSISTANT:
			role = "assistant"
		case pbv1.MessageRole_CHAT_COMPLETION_MESSAGE_ROLE_UNSPECIFIED:
			return pbv1.ErrorInvalidArgument("角色未指定")
		default:
			return pbv1.ErrorInvalidArgument("无效的角色: %s", msg.GetRole().String())
		}

		content := strings.TrimSpace(msg.GetContent())
		if content == "" {
			return pbv1.ErrorInvalidArgument("消息内容为空")
		}

		msgs = append(msgs, perplexity.Message{
			Role:    role,
			Content: content,
		})
	}

	if len(msgs) == 0 {
		return pbv1.ErrorInvalidArgument("至少需要一条消息")
	}

	s.log.Debugw("msg", "消息转换完成", "count", len(msgs))

	// 3. 构建请求 - 使用简洁模式
	chatReq := perplexity.ChatCompletionRequest{
		Model:      req.GetModel(),
		Messages:   msgs,
		Stream:     true,
		StreamMode: "concise", // 使用简洁模式
	}

	if req.Temperature != nil {
		chatReq.Temperature = req.Temperature
		s.log.Debugw("msg", "设置温度参数", "value", req.GetTemperature())
	}

	if req.TopP != nil {
		chatReq.TopP = req.TopP
		s.log.Debugw("msg", "设置 top_p 参数", "value", req.GetTopP())
	}

	// 4. 调用 Perplexity API
	stream, err := s.client.StreamChatCompletions(conn.Context(), token, chatReq)
	if err != nil {
		s.log.Errorw("msg", "调用 Perplexity API 失败", "error", err)
		return pbv1.ErrorUpstreamApiError("API 调用失败: %s", err.Error())
	}
	defer stream.Close()

	s.log.Infow("msg", "SSE 连接已建立")

	// 用于跟踪 think 标签的状态
	var (
		inThinkTag     bool
		thinkContent   strings.Builder
		currentChunkID string
		currentModel   string
		currentCreated int64
		partialTag     string // 保存可能被截断的标签
	)

	// 5. 处理流式响应
	for stream.Next() {
		chunk := stream.Current()

		s.log.Debugw("msg", "收到 chunk", "type", chunk.Object, "id", chunk.ID)

		// 记录当前 chunk 信息（用于 reasoning chunk）
		if chunk.ID != "" {
			currentChunkID = chunk.ID
		}
		if chunk.Model != "" {
			currentModel = chunk.Model
		}
		if chunk.Created > 0 {
			currentCreated = chunk.Created
		}

		// 特殊处理 completion chunk 以提取 think 标签
		if chunk.Object == "chat.completion.chunk" && len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
			content := chunk.Choices[0].Delta.Content
			if content == "" {
				continue
			}

			// 合并上次保存的部分标签
			if partialTag != "" {
				content = partialTag + content
				partialTag = ""
			}

			// 检查内容结尾是否可能是被截断的标签
			newPartialTag := s.extractPartialTag(content, inThinkTag)
			if newPartialTag != "" {
				// 保存可能的部分标签，从内容中移除
				partialTag = newPartialTag
				content = content[:len(content)-len(newPartialTag)]
				s.log.Debugw("msg", "检测到可能的部分标签", "partial", partialTag)
			}

			// 如果移除部分标签后内容为空，继续等待下一个 chunk
			if content == "" {
				continue
			}

			responses := s.extractThinkTags(content, &inThinkTag, &thinkContent, currentChunkID, currentModel, currentCreated)
			for _, pbResp := range responses {
				if pbResp != nil {
					if err := conn.Send(pbResp); err != nil {
						s.log.Errorw("msg", "发送响应失败", "error", err)
						return pbv1.ErrorUpstreamApiError("流发送错误: %s", err.Error())
					}
				}
			}
			continue
		}

		// 处理其他类型的 chunk
		pbResp, err := s.processChunk(&chunk)
		if err != nil {
			s.log.Errorw("msg", "处理 chunk 失败", "error", err, "type", chunk.Object)
			return pbv1.ErrorUpstreamApiError("chunk 处理错误: %s", err.Error())
		}

		// 如果没有需要发送的内容（空 chunk），跳过
		if pbResp == nil {
			continue
		}

		// 发送到客户端
		if err := conn.Send(pbResp); err != nil {
			s.log.Errorw("msg", "发送响应失败", "error", err)
			return pbv1.ErrorUpstreamApiError("流发送错误: %s", err.Error())
		}
	}

	// 6. 检查流错误
	if err := stream.Err(); err != nil {
		s.log.Errorw("msg", "流处理错误", "error", err)
		return pbv1.ErrorUpstreamApiError("流处理错误: %s", err.Error())
	}

	s.log.Infow("msg", "流式对话补全完成", "model", req.GetModel())

	return nil
}

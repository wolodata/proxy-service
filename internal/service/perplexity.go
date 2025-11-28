package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-kratos/kratos/v2/log"

	pbv1 "github.com/wolodata/proxy-service/api/proxy/v1"
	"github.com/wolodata/proxy-service/internal/client/perplexity"
	"github.com/wolodata/proxy-service/internal/converter"
	"github.com/wolodata/proxy-service/internal/ssestream"
)

type PerplexityService struct {
	pbv1.UnimplementedPerplexityServer
	log *log.Helper
}

func NewPerplexityService(logger log.Logger) *PerplexityService {
	return &PerplexityService{
		log: log.NewHelper(logger),
	}
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
		Id:            chunk.ID,
		Model:         chunk.Model,
		Created:       chunk.Created,
		SearchResults: converter.ConvertSearchResults(chunk.SearchResults),
		Images:        converter.ConvertImageResults(chunk.Images),
		Usage:         converter.ConvertUsage(chunk.Usage),
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

	// 4. 序列化请求体
	reqBody, err := json.Marshal(chatReq)
	if err != nil {
		s.log.Errorw("msg", "序列化请求失败", "error", err)
		return pbv1.ErrorInvalidArgument("请求序列化失败: %s", err.Error())
	}

	s.log.Debugw("msg", "请求体", "body", string(reqBody))

	// 5. 创建 HTTP 请求
	httpReq, err := http.NewRequestWithContext(
		conn.Context(),
		http.MethodPost,
		"https://api.perplexity.ai/chat/completions",
		bytes.NewReader(reqBody),
	)
	if err != nil {
		s.log.Errorw("msg", "创建 HTTP 请求失败", "error", err)
		return pbv1.ErrorUpstreamApiError("创建请求失败: %s", err.Error())
	}

	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	// 6. 发送请求
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		s.log.Errorw("msg", "HTTP 请求失败", "error", err)
		return pbv1.ErrorUpstreamApiError("API 请求失败: %s", err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.log.Errorw("msg", "API 返回错误状态码", "status", resp.StatusCode)
		return pbv1.ErrorUpstreamApiError("API 返回错误状态码: %d", resp.StatusCode)
	}

	s.log.Infow("msg", "SSE 连接已建立")

	// 7. 使用 ssestream 处理流式响应
	decoder := ssestream.NewDecoder(resp)
	stream := ssestream.NewStream[perplexity.ConciseChunk](decoder, nil)
	defer stream.Close()

	// 8. 处理流式响应
	for stream.Next() {
		chunk := stream.Current()

		s.log.Debugw("msg", "收到 chunk", "type", chunk.Object, "id", chunk.ID)

		// 处理 chunk 并转换为 proto 格式
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

	// 9. 检查流错误
	if err := stream.Err(); err != nil {
		s.log.Errorw("msg", "流处理错误", "error", err)
		return pbv1.ErrorUpstreamApiError("流处理错误: %s", err.Error())
	}

	s.log.Infow("msg", "流式对话补全完成", "model", req.GetModel())

	return nil
}

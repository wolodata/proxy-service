package service

import (
	"strings"
	"sync"

	"github.com/go-kratos/kratos/v2/log"

	perplexity "github.com/sgaunet/perplexity-go/v2"

	pbv1 "github.com/wolodata/proxy-service/api/proxy/v1"
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

// createClient 创建 Perplexity 客户端并验证参数
func (s *PerplexityService) createClient(url, token string) (*perplexity.Client, error) {
	if strings.TrimSpace(url) == "" {
		return nil, pbv1.ErrorInvalidArgument("url 为空")
	}

	if strings.TrimSpace(token) == "" {
		return nil, pbv1.ErrorInvalidArgument("token 为空")
	}

	client := perplexity.NewClient(token)

	return client, nil
}

func (s *PerplexityService) StreamChatCompletions(req *pbv1.StreamChatCompletionsRequest, conn pbv1.Perplexity_StreamChatCompletionsServer) error {
	s.log.Infow(
		"msg", "流式对话补全开始",
		"model", req.GetModel(),
		"message_count", len(req.GetMessages()),
		"temperature", req.GetTemperature(),
		"top_p", req.GetTopP(),
	)

	// 1. 创建客户端
	client, err := s.createClient(req.GetUrl(), req.GetToken())
	if err != nil {
		s.log.Errorw("msg", "创建客户端失败", "error", err)
		return err
	}

	// 2. 将消息从 gRPC 格式转换为 perplexity-go 格式
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

	s.log.Debugw("msg", "消息转换完成", "count", len(msgs))

	// 3. 构建补全请求
	completionRequest := perplexity.NewCompletionRequest(
		perplexity.WithModel(req.GetModel()),
		perplexity.WithMessages(msgs),
		perplexity.WithStream(true),
	)

	// 添加可选参数
	if req.Temperature != nil {
		completionRequest.Temperature = req.GetTemperature()
		s.log.Debugw("msg", "设置温度参数", "value", req.GetTemperature())
	}

	if req.TopP != nil {
		completionRequest.TopP = req.GetTopP()
		s.log.Debugw("msg", "设置 top_p 参数", "value", req.GetTopP())
	}

	// 4. 验证请求
	validator := perplexity.NewRequestValidator()
	if err := validator.ValidateRequest(completionRequest); err != nil {
		s.log.Errorw("msg", "请求验证失败", "error", err)
		return pbv1.ErrorInvalidArgument("验证失败: %s", err.Error())
	}

	// 5. 设置流式传输
	var wg sync.WaitGroup
	chResponses := make(chan perplexity.CompletionResponse, 5)
	streamCtx := conn.Context()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := client.SendSSEHTTPRequestWithContext(streamCtx, &wg, completionRequest, chResponses); err != nil {
			s.log.Errorw("msg", "SSE 请求失败", "error", err)
		}
	}()

	s.log.Infow("msg", "SSE 请求已发起")

	// 等待 goroutine 完成后关闭通道
	go func() {
		wg.Wait()
		close(chResponses)
		s.log.Debugw("msg", "响应通道已关闭")
	}()

	// 6. 使用状态机处理流式响应
	var lastContent string
	inThinking := false

	for response := range chResponses {
		if len(response.Choices) == 0 {
			continue
		}

		// 获取当前累积的内容
		currentContent := response.GetLastContent()

		// 计算增量内容（差分）
		if len(currentContent) <= len(lastContent) {
			continue // 没有新内容
		}

		newContent := currentContent[len(lastContent):]
		s.log.Debugw("msg", "收到增量内容", "length", len(newContent), "in_thinking", inThinking)

		// 状态机：检测 <think> 和 </think> 标签
		// 逐字符处理新内容以处理标签
		var reasoningBuf strings.Builder
		var messageBuf strings.Builder

		i := 0
		for i < len(newContent) {
			// 检查 <think> 标签
			if !inThinking && strings.HasPrefix(newContent[i:], "<think>") {
				inThinking = true
				i += len("<think>")
				s.log.Debugw("msg", "进入思考模式")
				continue
			}

			// 检查 </think> 标签
			if inThinking && strings.HasPrefix(newContent[i:], "</think>") {
				inThinking = false
				i += len("</think>")
				s.log.Debugw("msg", "退出思考模式")
				continue
			}

			// 根据当前状态累积内容
			if inThinking {
				reasoningBuf.WriteByte(newContent[i])
			} else {
				messageBuf.WriteByte(newContent[i])
			}
			i++
		}

		// 统一发送当前 chunk 累积的内容
		if reasoningBuf.Len() > 0 {
			if err := conn.Send(&pbv1.StreamChatCompletionsResponse{
				Content: &pbv1.StreamChatCompletionsResponse_ReasoningChunk{
					ReasoningChunk: reasoningBuf.String(),
				},
			}); err != nil {
				s.log.Errorw("msg", "发送推理块失败", "error", err)
				return pbv1.ErrorUpstreamApiError("流发送错误: %s", err.Error())
			}
			reasoningBuf.Reset()
		}

		if messageBuf.Len() > 0 {
			if err := conn.Send(&pbv1.StreamChatCompletionsResponse{
				Content: &pbv1.StreamChatCompletionsResponse_MessageChunk{
					MessageChunk: messageBuf.String(),
				},
			}); err != nil {
				s.log.Errorw("msg", "发送消息块失败", "error", err)
				return pbv1.ErrorUpstreamApiError("流发送错误: %s", err.Error())
			}
			messageBuf.Reset()
		}

		lastContent = currentContent
	}

	s.log.Infow(
		"msg", "流式对话补全完成",
		"model", req.GetModel(),
		"total_content_length", len(lastContent),
	)

	return nil
}

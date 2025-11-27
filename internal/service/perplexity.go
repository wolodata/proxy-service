package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-kratos/kratos/v2/log"

	pbv1 "github.com/wolodata/proxy-service/api/proxy/v1"
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

// Perplexity API 请求结构
type ChatCompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream"`
	Temperature *float64  `json:"temperature,omitempty"`
	TopP        *float64  `json:"top_p,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Perplexity API 响应结构
type ChatCompletionChunk struct {
	ID            string         `json:"id"`
	Object        string         `json:"object"`
	Created       int64          `json:"created"`
	Model         string         `json:"model"`
	Usage         *Usage         `json:"usage,omitempty"`
	Citations     []string       `json:"citations,omitempty"`
	SearchResults []SearchResult `json:"search_results,omitempty"`
	Choices       []Choice       `json:"choices"`
}

type Usage struct {
	PromptTokens      int    `json:"prompt_tokens"`
	CompletionTokens  int    `json:"completion_tokens"`
	TotalTokens       int    `json:"total_tokens"`
	SearchContextSize string `json:"search_context_size,omitempty"`
	// sonar-deep-research 模型的额外字段
	CitationTokens   int `json:"citation_tokens,omitempty"`
	NumSearchQueries int `json:"num_search_queries,omitempty"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
}

type SearchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Date        string `json:"date,omitempty"`
	LastUpdated string `json:"last_updated,omitempty"`
	Snippet     string `json:"snippet"`
	Source      string `json:"source"`
}

type Choice struct {
	Index        int      `json:"index"`
	Message      *Message `json:"message,omitempty"` // 累积式完整消息
	Delta        Delta    `json:"delta"`             // 增量消息
	FinishReason *string  `json:"finish_reason"`
}

type Delta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// isPotentialTagPrefix 检查字符串是否可能是标签的前缀
func (s *PerplexityService) isPotentialTagPrefix(str string, inThinking bool) bool {
	if len(str) == 0 || len(str) >= 8 {
		return false
	}

	// 根据当前状态检查可能的标签
	if inThinking {
		// 在 thinking 模式下，检查是否是 </think> 的前缀
		closeTag := "</think>"
		return strings.HasPrefix(closeTag, str)
	} else {
		// 不在 thinking 模式下，检查是否是 <think> 的前缀
		openTag := "<think>"
		return strings.HasPrefix(openTag, str)
	}
}

func (s *PerplexityService) StreamChatCompletions(req *pbv1.StreamChatCompletionsRequest, conn pbv1.Perplexity_StreamChatCompletionsServer) error {
	s.log.Infow(
		"msg", "流式对话补全开始",
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
	msgs := make([]Message, 0, len(req.GetMessages()))
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

		msgs = append(msgs, Message{
			Role:    role,
			Content: content,
		})
	}

	s.log.Debugw("msg", "消息转换完成", "count", len(msgs))

	if len(req.GetMessages()) == 0 {
		return pbv1.ErrorInvalidArgument("至少需要一条消息")
	}

	// 3. 构建请求
	chatReq := ChatCompletionRequest{
		Model:    req.GetModel(),
		Messages: msgs,
		Stream:   true,
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

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		s.log.Errorw("msg", "API 返回错误状态码", "status", resp.StatusCode)
		return pbv1.ErrorUpstreamApiError("API 返回错误状态码: %d", resp.StatusCode)
	}

	s.log.Infow("msg", "SSE 连接已建立")

	// 7. 使用 ssestream 处理流式响应
	decoder := ssestream.NewDecoder(resp)
	stream := ssestream.NewStream[ChatCompletionChunk](decoder, nil)
	defer stream.Close()

	// 8. 使用状态机处理流式响应
	inThinking := false
	var buffer strings.Builder // 缓冲区，用于处理跨chunk的标签

	for stream.Next() {
		chunk := stream.Current()

		// 记录 usage 信息（仅用于日志）
		if chunk.Usage != nil {
			s.log.Debugw("msg", "收到 usage 信息", "usage", chunk.Usage)
		}

		// 准备元数据（citations 和 search_results）
		var citations []string
		var searchResults []*pbv1.SearchResult

		if chunk.Citations != nil {
			citations = chunk.Citations
		}

		if chunk.SearchResults != nil {
			searchResults = make([]*pbv1.SearchResult, 0, len(chunk.SearchResults))
			for _, sr := range chunk.SearchResults {
				pbSr := &pbv1.SearchResult{
					Title:       sr.Title,
					Url:         sr.URL,
					Date:        nil,
					LastUpdated: nil,
					Snippet:     sr.Snippet,
					Source:      sr.Source,
				}
				if sr.Date != "" {
					pbSr.Date = &sr.Date
				}
				if sr.LastUpdated != "" {
					pbSr.LastUpdated = &sr.LastUpdated
				}
				searchResults = append(searchResults, pbSr)
			}
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		// 获取增量内容
		deltaContent := chunk.Choices[0].Delta.Content
		if deltaContent == "" {
			continue
		}

		s.log.Debugw("msg", "收到增量内容", "length", len(deltaContent), "in_thinking", inThinking)

		// 将缓冲区内容与当前chunk合并处理
		if buffer.Len() > 0 {
			deltaContent = buffer.String() + deltaContent
			buffer.Reset()
		}

		// 状态机：逐字符处理以检测 <think> 和 </think> 标签
		var reasoningBuf strings.Builder
		var messageBuf strings.Builder

		i := 0
		for i < len(deltaContent) {
			// 检查 <think> 标签
			if !inThinking && strings.HasPrefix(deltaContent[i:], "<think>") {
				inThinking = true
				i += len("<think>")
				s.log.Debugw("msg", "进入思考模式")
				continue
			}

			// 检查 </think> 标签
			if inThinking && strings.HasPrefix(deltaContent[i:], "</think>") {
				inThinking = false
				i += len("</think>")
				s.log.Debugw("msg", "退出思考模式")
				continue
			}

			// 检查是否接近chunk末尾，且可能有部分标签
			remaining := deltaContent[i:]
			if len(remaining) < 8 { // </think> 最长8字符
				// 检查是否是标签的部分前缀
				if s.isPotentialTagPrefix(remaining, inThinking) {
					// 保存到缓冲区，等待下一个chunk
					buffer.WriteString(remaining)
					s.log.Debugw("msg", "检测到可能的部分标签，保存到缓冲区", "buffer", remaining)
					break
				}
			}

			// 根据当前状态累积内容
			if inThinking {
				reasoningBuf.WriteByte(deltaContent[i])
			} else {
				messageBuf.WriteByte(deltaContent[i])
			}
			i++
		}

		// 发送推理块（带元数据）
		if reasoningBuf.Len() > 0 {
			if err := conn.Send(&pbv1.StreamChatCompletionsResponse{
				Content: &pbv1.StreamChatCompletionsResponse_ReasoningChunk{
					ReasoningChunk: reasoningBuf.String(),
				},
				Citations:     citations,
				SearchResults: searchResults,
			}); err != nil {
				s.log.Errorw("msg", "发送推理块失败", "error", err)
				return pbv1.ErrorUpstreamApiError("流发送错误: %s", err.Error())
			}
		}

		// 发送消息块（带元数据）
		if messageBuf.Len() > 0 {
			if err := conn.Send(&pbv1.StreamChatCompletionsResponse{
				Content: &pbv1.StreamChatCompletionsResponse_MessageChunk{
					MessageChunk: messageBuf.String(),
				},
				Citations:     citations,
				SearchResults: searchResults,
			}); err != nil {
				s.log.Errorw("msg", "发送消息块失败", "error", err)
				return pbv1.ErrorUpstreamApiError("流发送错误: %s", err.Error())
			}
		}
	}

	// 处理缓冲区中剩余的内容（如果有）
	if buffer.Len() > 0 {
		remaining := buffer.String()
		s.log.Debugw("msg", "处理缓冲区剩余内容", "content", remaining)

		// 将剩余内容作为普通文本发送
		var resp *pbv1.StreamChatCompletionsResponse
		if inThinking {
			resp = &pbv1.StreamChatCompletionsResponse{
				Content: &pbv1.StreamChatCompletionsResponse_ReasoningChunk{
					ReasoningChunk: remaining,
				},
			}
		} else {
			resp = &pbv1.StreamChatCompletionsResponse{
				Content: &pbv1.StreamChatCompletionsResponse_MessageChunk{
					MessageChunk: remaining,
				},
			}
		}

		if err := conn.Send(resp); err != nil {
			s.log.Errorw("msg", "发送缓冲区内容失败", "error", err)
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

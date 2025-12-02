package perplexity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/wolodata/proxy-service/internal/ssestream"
)

const (
	// DefaultBaseURL Perplexity API 的默认地址
	DefaultBaseURL = "https://api.perplexity.ai/chat/completions"
)

// Client Perplexity API 客户端
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient 创建新的 Perplexity API 客户端
func NewClient() *Client {
	return &Client{
		baseURL:    DefaultBaseURL,
		httpClient: http.DefaultClient,
	}
}

// NewClientWithHTTPClient 创建带自定义 HTTP 客户端的 Perplexity API 客户端
func NewClientWithHTTPClient(httpClient *http.Client) *Client {
	return &Client{
		baseURL:    DefaultBaseURL,
		httpClient: httpClient,
	}
}

// SetBaseURL 设置 API 地址（用于测试）
func (c *Client) SetBaseURL(url string) {
	c.baseURL = url
}

// StreamChatCompletions 调用 Perplexity API 流式接口
// 返回一个可迭代的 chunk 流和错误
func (c *Client) StreamChatCompletions(ctx context.Context, token string, req ChatCompletionRequest) (*ChunkStream, error) {
	req.Stream = true
	req.StreamMode = "concise"

	if req.Model != "sonar" && req.Model != "sonar-deep-research" {
		return nil, fmt.Errorf("不支持的模型: %s", req.Model)
	}

	// 1. 序列化请求体
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	// 2. 创建 HTTP 请求
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	// 3. 发送请求
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}

	// 4. 检查状态码
	if resp.StatusCode != http.StatusOK {
		// 读取错误响应体
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		return nil, fmt.Errorf("API 返回错误状态码 %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// 5. 创建 SSE 流
	decoder := ssestream.NewDecoder(resp)
	stream := ssestream.NewStream[ConciseChunk](decoder, nil)

	return &ChunkStream{
		stream:   stream,
		response: resp,
	}, nil
}

// ChunkStream 封装 SSE 流，提供迭代接口
type ChunkStream struct {
	stream   *ssestream.Stream[ConciseChunk]
	response *http.Response
}

// Next 获取下一个 chunk
func (s *ChunkStream) Next() bool {
	return s.stream.Next()
}

// Current 获取当前 chunk
func (s *ChunkStream) Current() ConciseChunk {
	return s.stream.Current()
}

// Err 获取流错误
func (s *ChunkStream) Err() error {
	return s.stream.Err()
}

// Close 关闭流和底层连接
func (s *ChunkStream) Close() error {
	s.stream.Close()
	return s.response.Body.Close()
}

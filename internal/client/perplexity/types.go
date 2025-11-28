package perplexity

// ChatCompletionRequest Perplexity API 请求结构
type ChatCompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream"`
	StreamMode  string    `json:"stream_mode,omitempty"` // "concise" 或 "full"
	Temperature *float64  `json:"temperature,omitempty"`
	TopP        *float64  `json:"top_p,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ConciseChunk Perplexity API 简洁模式响应结构
type ConciseChunk struct {
	ID            string          `json:"id"`
	Object        string          `json:"object"` // chunk 类型标识
	Created       int64           `json:"created"`
	Model         string          `json:"model"`
	Choices       []ConciseChoice `json:"choices,omitempty"`
	SearchResults []SearchResult  `json:"search_results,omitempty"`
	Images        []ImageResult   `json:"images,omitempty"`
	Usage         *Usage          `json:"usage,omitempty"`
}

type ConciseChoice struct {
	Index        int             `json:"index"`
	Delta        *ConciseDelta   `json:"delta,omitempty"`   // 用于 reasoning 和 completion.chunk
	Message      *ConciseMessage `json:"message,omitempty"` // 用于 reasoning.done 和 completion.done
	FinishReason *string         `json:"finish_reason,omitempty"`
}

type ConciseDelta struct {
	Content        string          `json:"content,omitempty"`
	ReasoningSteps []ReasoningStep `json:"reasoning_steps,omitempty"`
}

type ConciseMessage struct {
	Role           string          `json:"role,omitempty"`
	Content        string          `json:"content,omitempty"`
	ReasoningSteps []ReasoningStep `json:"reasoning_steps,omitempty"`
}

type ReasoningStep struct {
	Thought   string     `json:"thought"`
	Type      string     `json:"type"`
	WebSearch *WebSearch `json:"web_search,omitempty"`
}

type WebSearch struct {
	SearchKeywords []string       `json:"search_keywords,omitempty"`
	SearchResults  []SearchResult `json:"search_results,omitempty"`
}

type SearchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Date        string `json:"date,omitempty"`
	LastUpdated string `json:"last_updated,omitempty"`
	Snippet     string `json:"snippet"`
	Source      string `json:"source"`
}

type ImageResult struct {
	URL    string `json:"url"`
	Title  string `json:"title,omitempty"`
	Source string `json:"source,omitempty"`
}

type Usage struct {
	PromptTokens      int   `json:"prompt_tokens,omitempty"`
	CompletionTokens  int   `json:"completion_tokens,omitempty"`
	TotalTokens       int   `json:"total_tokens,omitempty"`
	SearchContextSize int   `json:"search_context_size,omitempty"`
	Cost              *Cost `json:"cost,omitempty"`
}

type Cost struct {
	InputTokensCost  float64 `json:"input_tokens_cost,omitempty"`
	OutputTokensCost float64 `json:"output_tokens_cost,omitempty"`
	RequestCost      float64 `json:"request_cost,omitempty"`
	TotalCost        float64 `json:"total_cost,omitempty"`
}

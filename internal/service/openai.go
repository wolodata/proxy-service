package service

import (
	"context"
	"strings"

	"github.com/davecgh/go-spew/spew"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	pb "github.com/wolodata/proxy-service/api/proxy/v1"
)

type OpenAIService struct {
	pb.UnimplementedOpenAIServer
	log *log.Helper
}

func NewOpenAIService(logger log.Logger) *OpenAIService {
	return &OpenAIService{
		log: log.NewHelper(logger),
	}
}

// createClient creates an OpenAI client with validation
func (s *OpenAIService) createClient(url, token string) (*openai.Client, error) {
	if strings.TrimSpace(url) == "" {
		return nil, pb.ErrorInvalidArgument("url is empty")
	}

	if strings.TrimSpace(token) == "" {
		return nil, pb.ErrorInvalidArgument("token is empty")
	}

	client := openai.NewClient(
		option.WithBaseURL(url),
		option.WithAPIKey(token),
	)
	return &client, nil
}

// buildChatParams builds chat completion parameters with validation
func (s *OpenAIService) buildChatParams(messages []openai.ChatCompletionMessageParamUnion, model openai.ChatModel, temperature, topP float32) (openai.ChatCompletionNewParams, error) {
	if strings.TrimSpace(model) == "" {
		return openai.ChatCompletionNewParams{}, pb.ErrorInvalidArgument("model is empty")
	}

	params := openai.ChatCompletionNewParams{
		Messages: messages,
		Model:    model,
	}

	// Add optional parameters only if provided (non-zero values)
	// Note: This means 0 cannot be explicitly set for these parameters
	if temperature != 0 {
		if temperature < 0 || temperature > 2.0 {
			return openai.ChatCompletionNewParams{}, pb.ErrorInvalidArgument("temperature must be between 0 and 2.0, got: %f", temperature)
		}
		params.Temperature = openai.Float(float64(temperature))
	}

	if topP != 0 {
		if topP < 0 || topP > 1.0 {
			return openai.ChatCompletionNewParams{}, pb.ErrorInvalidArgument("top_p must be between 0 and 1.0, got: %f", topP)
		}
		params.TopP = openai.Float(float64(topP))
	}

	return params, nil
}

// convertMessages converts protobuf messages to OpenAI message format with validation
func (s *OpenAIService) convertMessages(messages []*pb.ChatCompletionMessage) ([]openai.ChatCompletionMessageParamUnion, error) {
	if len(messages) == 0 {
		return nil, pb.ErrorInvalidArgument("messages array is empty")
	}

	result := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))

	for _, v := range messages {
		content := strings.TrimSpace(v.GetContent())
		if content == "" {
			return nil, pb.ErrorInvalidArgument("content is empty")
		}

		var msg openai.ChatCompletionMessageParamUnion
		switch v.GetRole() {
		case pb.ChatCompletionMessageRole_CHAT_COMPLETION_MESSAGE_ROLE_SYSTEM:
			msg = openai.SystemMessage(v.GetContent())
		case pb.ChatCompletionMessageRole_CHAT_COMPLETION_MESSAGE_ROLE_USER:
			msg = openai.UserMessage(v.GetContent())
		case pb.ChatCompletionMessageRole_CHAT_COMPLETION_MESSAGE_ROLE_ASSISTANT:
			msg = openai.AssistantMessage(v.GetContent())
		case pb.ChatCompletionMessageRole_CHAT_COMPLETION_MESSAGE_ROLE_UNSPECIFIED:
			return nil, pb.ErrorInvalidArgument("role is unspecified")
		default:
			return nil, pb.ErrorInvalidArgument("invalid role: %s", v.GetRole().String())
		}

		result = append(result, msg)
	}

	return result, nil
}

func (s *OpenAIService) ChatCompletion(ctx context.Context, req *pb.ChatCompletionRequest) (*pb.ChatCompletionResponse, error) {
	s.log.Infow(
		"msg", "ChatCompletion started",
		"model", req.GetModel(),
		"message_count", len(req.GetMessages()),
		"temperature", req.GetTemperature(),
		"top_p", req.GetTopP(),
	)

	client, err := s.createClient(req.GetUrl(), req.GetToken())
	if err != nil {
		s.log.Errorw("msg", "Failed to create client", "error", err)
		return nil, err
	}

	messages, err := s.convertMessages(req.GetMessages())
	if err != nil {
		s.log.Errorw("msg", "Failed to convert messages", "error", err)
		return nil, err
	}

	params, err := s.buildChatParams(messages, req.GetModel(), req.GetTemperature(), req.GetTopP())
	if err != nil {
		s.log.Errorw("msg", "Failed to build params", "error", err)
		return nil, err
	}

	response, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		s.log.Errorw("msg", "OpenAI API error", "error", err, "model", req.GetModel())
		return nil, pb.ErrorUpstreamApiError("CreateChatCompletion error: %s", err.Error())
	}

	if len(response.Choices) == 0 {
		s.log.Errorw("msg", "No choices in response", "response", spew.Sdump(response))
		noChoiceErr := pb.ErrorNoChoice("no choices in response")
		noChoiceErr = noChoiceErr.WithMetadata(map[string]string{
			"response": spew.Sdump(response),
		})
		return nil, noChoiceErr
	}

	res := strings.TrimSpace(response.Choices[0].Message.Content)

	s.log.Infow(
		"msg", "ChatCompletion completed",
		"model", req.GetModel(),
		"response_length", len(res),
	)

	return &pb.ChatCompletionResponse{
		Content: res,
	}, nil
}

func (s *OpenAIService) StreamChatCompletion(req *pb.StreamChatCompletionRequest, conn pb.OpenAI_StreamChatCompletionServer) error {
	s.log.Infow(
		"msg", "StreamChatCompletion started",
		"model", req.GetModel(),
		"message_count", len(req.GetMessages()),
		"temperature", req.GetTemperature(),
		"top_p", req.GetTopP(),
	)

	client, err := s.createClient(req.GetUrl(), req.GetToken())
	if err != nil {
		s.log.Errorw("msg", "Failed to create client", "error", err)
		return err
	}

	messages, err := s.convertMessages(req.GetMessages())
	if err != nil {
		s.log.Errorw("msg", "Failed to convert messages", "error", err)
		return err
	}

	params, err := s.buildChatParams(messages, req.GetModel(), req.GetTemperature(), req.GetTopP())
	if err != nil {
		s.log.Errorw("msg", "Failed to build params", "error", err)
		return err
	}

	streamCtx := conn.Context()
	stream := client.Chat.Completions.NewStreaming(streamCtx, params)
	defer stream.Close()

	acc := openai.ChatCompletionAccumulator{}

	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)

		if len(chunk.Choices) == 0 {
			s.log.Errorw("msg", "No choices in stream chunk", "chunk", spew.Sdump(chunk))
			noChoiceErr := pb.ErrorNoChoice("no choices in stream chunk")
			noChoiceErr = noChoiceErr.WithMetadata(map[string]string{
				"response": spew.Sdump(chunk),
			})
			return noChoiceErr
		}

		if err := conn.Send(&pb.StreamChatCompletionResponse{
			Chunk: chunk.Choices[0].Delta.Content,
		}); err != nil {
			s.log.Errorw("msg", "Failed to send stream chunk", "error", err)
			return pb.ErrorUpstreamApiError("stream send error: %s", err.Error())
		}
	}

	if err := stream.Err(); err != nil {
		s.log.Errorw("msg", "Stream error", "error", err, "model", req.GetModel())
		return pb.ErrorUpstreamApiError("stream error: %s", err.Error())
	}

	// Get the complete response from accumulator
	var fullContent string
	if len(acc.Choices) > 0 {
		fullContent = acc.Choices[0].Message.Content
	}

	s.log.Infow(
		"msg", "StreamChatCompletion completed",
		"model", req.GetModel(),
		"response_length", len(fullContent),
		"response_content", fullContent,
	)

	return nil
}

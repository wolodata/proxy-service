package service

import (
	"strings"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"

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

// buildResponsesParams builds responses API parameters with validation
func (s *OpenAIService) buildResponsesParams(messages []*pb.Message, model string, temperature, topP float64) (responses.ResponseNewParams, error) {
	if strings.TrimSpace(model) == "" {
		return responses.ResponseNewParams{}, pb.ErrorInvalidArgument("model is empty")
	}

	if len(messages) == 0 {
		return responses.ResponseNewParams{}, pb.ErrorInvalidArgument("messages array is empty")
	}

	// Convert messages to input items
	inputItems := make([]responses.ResponseInputItemUnionParam, 0, len(messages))
	for _, msg := range messages {
		content := strings.TrimSpace(msg.GetContent())
		if content == "" {
			return responses.ResponseNewParams{}, pb.ErrorInvalidArgument("content is empty")
		}

		// Convert role
		var role responses.EasyInputMessageRole
		switch msg.GetRole() {
		case pb.MessageRole_CHAT_COMPLETION_MESSAGE_ROLE_SYSTEM:
			// Responses API doesn't have system role, use user role
			role = responses.EasyInputMessageRoleUser
		case pb.MessageRole_CHAT_COMPLETION_MESSAGE_ROLE_USER:
			role = responses.EasyInputMessageRoleUser
		case pb.MessageRole_CHAT_COMPLETION_MESSAGE_ROLE_ASSISTANT:
			role = responses.EasyInputMessageRoleAssistant
		case pb.MessageRole_CHAT_COMPLETION_MESSAGE_ROLE_UNSPECIFIED:
			return responses.ResponseNewParams{}, pb.ErrorInvalidArgument("role is unspecified")
		default:
			return responses.ResponseNewParams{}, pb.ErrorInvalidArgument("invalid role: %s", msg.GetRole().String())
		}

		inputItems = append(inputItems, responses.ResponseInputItemParamOfMessage(content, role))
	}

	// Create params
	params := responses.ResponseNewParams{
		Model: model,
	}

	// Set input
	params.Input.OfInputItemList = inputItems

	// Add optional parameters
	if temperature != 0 {
		if temperature < 0 || temperature > 2.0 {
			return responses.ResponseNewParams{}, pb.ErrorInvalidArgument("temperature must be between 0 and 2.0, got: %f", temperature)
		}
		params.Temperature = openai.Float(temperature)
	}

	if topP != 0 {
		if topP < 0 || topP > 1.0 {
			return responses.ResponseNewParams{}, pb.ErrorInvalidArgument("top_p must be between 0 and 1.0, got: %f", topP)
		}
		params.TopP = openai.Float(topP)
	}

	return params, nil
}

func (s *OpenAIService) StreamResponsesCompletion(req *pb.StreamResponsesRequest, conn pb.OpenAI_StreamResponsesCompletionServer) error {
	s.log.Infow(
		"msg", "StreamResponsesCompletion started",
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

	// Build responses params
	params, err := s.buildResponsesParams(req.GetMessages(), req.GetModel(), req.GetTemperature(), req.GetTopP())
	if err != nil {
		s.log.Errorw("msg", "Failed to build params", "error", err)
		return err
	}

	streamCtx := conn.Context()
	stream := client.Responses.NewStreaming(streamCtx, params)
	defer stream.Close()

	for stream.Next() {
		event := stream.Current()

		// Handle different event types
		switch event.Type {
		case "response.reasoning_text.delta":
			// Send reasoning chunk
			if err := conn.Send(&pb.StreamResponsesResponse{
				Content: &pb.StreamResponsesResponse_ReasoningChunk{
					ReasoningChunk: event.Delta,
				},
			}); err != nil {
				s.log.Errorw("msg", "Failed to send reasoning chunk", "error", err)
				return pb.ErrorUpstreamApiError("stream send error: %s", err.Error())
			}

		case "response.output_text.delta":
			// Send message chunk
			if err := conn.Send(&pb.StreamResponsesResponse{
				Content: &pb.StreamResponsesResponse_MessageChunk{
					MessageChunk: event.Delta,
				},
			}); err != nil {
				s.log.Errorw("msg", "Failed to send message chunk", "error", err)
				return pb.ErrorUpstreamApiError("stream send error: %s", err.Error())
			}

		default:
			// Log unhandled event types for debugging
			s.log.Debugw("msg", "Unhandled event type", "type", event.Type, "sequence", event.SequenceNumber, "delta", event.Delta)
		}
	}

	if err := stream.Err(); err != nil {
		s.log.Errorw("msg", "Stream error", "error", err, "model", req.GetModel())
		return pb.ErrorUpstreamApiError("stream error: %s", err.Error())
	}

	s.log.Infow(
		"msg", "StreamResponsesCompletion completed",
		"model", req.GetModel(),
	)

	return nil
}

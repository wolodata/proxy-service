package service

import (
	"context"
	"github.com/davecgh/go-spew/spew"
	"github.com/go-kratos/kratos/v2/errors"
	"io"
	"strings"

	openai "github.com/sashabaranov/go-openai"

	pb "github.com/wolodata/proxy-service/api/proxy/v1"
)

type OpenAIService struct {
	pb.UnimplementedOpenAIServer
}

func NewOpenAIService() *OpenAIService {
	return &OpenAIService{}
}

func (s *OpenAIService) ChatCompletion(ctx context.Context, req *pb.ChatCompletionRequest) (*pb.ChatCompletionResponse, error) {
	cfg := openai.DefaultConfig(req.GetToken())
	cfg.BaseURL = req.GetUrl()

	client := openai.NewClientWithConfig(cfg)

	request := openai.ChatCompletionRequest{
		Model:       req.GetModel(),
		Messages:    make([]openai.ChatCompletionMessage, 0),
		Temperature: req.GetTemperature(),
		TopP:        req.GetTopP(),
	}

	for _, v := range req.GetMessages() {
		var role string
		switch v.GetRole() {
		case pb.ChatCompletionMessageRole_CHAT_COMPLETION_MESSAGE_ROLE_UNSPECIFIED:
			err := pb.ErrorInvalidRole("role: %s", v.GetRole().String())
			return nil, err
		case pb.ChatCompletionMessageRole_CHAT_COMPLETION_MESSAGE_ROLE_SYSTEM:
			role = openai.ChatMessageRoleSystem
		case pb.ChatCompletionMessageRole_CHAT_COMPLETION_MESSAGE_ROLE_USER:
			role = openai.ChatMessageRoleUser
		case pb.ChatCompletionMessageRole_CHAT_COMPLETION_MESSAGE_ROLE_ASSISTANT:
			role = openai.ChatMessageRoleAssistant
		}

		content := strings.TrimSpace(v.GetContent())
		if content == "" {
			err := pb.ErrorEmptyContent("content: %s", v.GetContent)
			return nil, err
		}

		request.Messages = append(request.Messages, openai.ChatCompletionMessage{
			Role:    role,
			Content: v.GetContent(),
		})
	}

	response, err := client.CreateChatCompletion(ctx, request)
	if err != nil {
		err := pb.ErrorOpenaiError("CreateChatCompletion error: %s", err.Error())
		return nil, err
	}

	if len(response.Choices) == 0 {
		err := pb.ErrorNoChoice("", nil)
		err = err.WithMetadata(map[string]string{
			"response": spew.Sdump(response),
		})
		return nil, err
	}

	res := strings.TrimSpace(response.Choices[0].Message.Content)

	return &pb.ChatCompletionResponse{
		Content: res,
	}, nil
}
func (s *OpenAIService) StreamChatCompletion(req *pb.StreamChatCompletionRequest, conn pb.OpenAI_StreamChatCompletionServer) error {
	cfg := openai.DefaultConfig(req.GetToken())
	cfg.BaseURL = req.GetUrl()

	client := openai.NewClientWithConfig(cfg)

	request := openai.ChatCompletionRequest{
		Model:       req.GetModel(),
		Messages:    make([]openai.ChatCompletionMessage, 0),
		Temperature: req.GetTemperature(),
		TopP:        req.GetTopP(),
	}

	for _, v := range req.GetMessages() {
		var role string
		switch v.GetRole() {
		case pb.ChatCompletionMessageRole_CHAT_COMPLETION_MESSAGE_ROLE_UNSPECIFIED:
			err := pb.ErrorInvalidRole("role: %s", v.GetRole().String())
			return err
		case pb.ChatCompletionMessageRole_CHAT_COMPLETION_MESSAGE_ROLE_SYSTEM:
			role = openai.ChatMessageRoleSystem
		case pb.ChatCompletionMessageRole_CHAT_COMPLETION_MESSAGE_ROLE_USER:
			role = openai.ChatMessageRoleUser
		case pb.ChatCompletionMessageRole_CHAT_COMPLETION_MESSAGE_ROLE_ASSISTANT:
			role = openai.ChatMessageRoleAssistant
		}

		content := strings.TrimSpace(v.GetContent())
		if content == "" {
			err := pb.ErrorEmptyContent("content: %s", v.GetContent)
			return err
		}

		request.Messages = append(request.Messages, openai.ChatCompletionMessage{
			Role:    role,
			Content: v.GetContent(),
		})
	}

	chatCompletionStream, err := client.CreateChatCompletionStream(context.TODO(), request)
	if err != nil {
		err := pb.ErrorOpenaiError("CreateChatCompletionStream error: %s", err.Error())
		return err
	}

	defer chatCompletionStream.Close()

	for {
		response, err := chatCompletionStream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			err := pb.ErrorOpenaiError("receive stream error: %s", err.Error())
			return err
		}

		if len(response.Choices) == 0 {
			err := pb.ErrorNoChoice("", nil)
			err = err.WithMetadata(map[string]string{
				"response": spew.Sdump(response),
			})
			return err
		}

		conn.Send(&pb.StreamChatCompletionResponse{
			Chunk: response.Choices[0].Delta.Content,
		})
	}
}

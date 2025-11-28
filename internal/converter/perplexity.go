package converter

import (
	pbv1 "github.com/wolodata/proxy-service/api/proxy/v1"
	"github.com/wolodata/proxy-service/internal/client/perplexity"
)

// ConvertSearchResults 转换搜索结果
func ConvertSearchResults(results []perplexity.SearchResult) []*pbv1.SearchResult {
	if results == nil {
		return nil
	}
	pbResults := make([]*pbv1.SearchResult, 0, len(results))
	for _, sr := range results {
		pbSr := &pbv1.SearchResult{
			Title:   sr.Title,
			Url:     sr.URL,
			Snippet: sr.Snippet,
			Source:  sr.Source,
		}
		if sr.Date != "" {
			pbSr.Date = &sr.Date
		}
		if sr.LastUpdated != "" {
			pbSr.LastUpdated = &sr.LastUpdated
		}
		pbResults = append(pbResults, pbSr)
	}
	return pbResults
}

// ConvertImageResults 转换图片结果
func ConvertImageResults(images []perplexity.ImageResult) []*pbv1.ImageResult {
	if images == nil {
		return nil
	}
	pbImages := make([]*pbv1.ImageResult, 0, len(images))
	for _, img := range images {
		pbImg := &pbv1.ImageResult{
			Url: img.URL,
		}
		if img.Title != "" {
			pbImg.Title = &img.Title
		}
		if img.Source != "" {
			pbImg.Source = &img.Source
		}
		pbImages = append(pbImages, pbImg)
	}
	return pbImages
}

// ConvertReasoningSteps 转换推理步骤
func ConvertReasoningSteps(steps []perplexity.ReasoningStep) []*pbv1.ReasoningStep {
	if steps == nil {
		return nil
	}
	pbSteps := make([]*pbv1.ReasoningStep, 0, len(steps))
	for _, step := range steps {
		pbStep := &pbv1.ReasoningStep{
			Thought: step.Thought,
			Type:    step.Type,
		}
		if step.WebSearch != nil {
			pbStep.WebSearch = &pbv1.WebSearch{
				SearchKeywords: step.WebSearch.SearchKeywords,
				SearchResults:  ConvertSearchResults(step.WebSearch.SearchResults),
			}
		}
		pbSteps = append(pbSteps, pbStep)
	}
	return pbSteps
}

// ConvertUsage 转换使用统计
func ConvertUsage(usage *perplexity.Usage) *pbv1.Usage {
	if usage == nil {
		return nil
	}
	pbUsage := &pbv1.Usage{}

	if usage.PromptTokens > 0 {
		pbUsage.PromptTokens = ptrInt32(int32(usage.PromptTokens))
	}
	if usage.CompletionTokens > 0 {
		pbUsage.CompletionTokens = ptrInt32(int32(usage.CompletionTokens))
	}
	if usage.TotalTokens > 0 {
		pbUsage.TotalTokens = ptrInt32(int32(usage.TotalTokens))
	}
	// SearchContextSize 是字符串类型 ("low", "medium", "high")，proto 中定义为 int32
	// 暂时跳过转换，因为无法直接映射
	// 如果需要，可以做映射: low=1, medium=2, high=3

	if usage.Cost != nil {
		if usage.Cost.InputTokensCost > 0 {
			pbUsage.InputTokensCost = &usage.Cost.InputTokensCost
		}
		if usage.Cost.OutputTokensCost > 0 {
			pbUsage.OutputTokensCost = &usage.Cost.OutputTokensCost
		}
		if usage.Cost.RequestCost > 0 {
			pbUsage.RequestCost = &usage.Cost.RequestCost
		}
	}

	return pbUsage
}

func ptrInt32(v int32) *int32 {
	return &v
}

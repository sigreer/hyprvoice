package llm

import (
	"context"
	"fmt"
)

// Config holds configuration for the LLM processor
type Config struct {
	Provider     string
	APIKey       string
	Model        string
	Level        string // "minimal", "moderate", "thorough", or "custom"
	CustomPrompt string // Used when Level is "custom"
}

// Processor processes transcribed text through an LLM
type Processor interface {
	Process(ctx context.Context, text string) (string, error)
}

// NewProcessor creates a new LLM processor based on the provider
func NewProcessor(config Config) (Processor, error) {
	switch config.Provider {
	case "openai":
		return NewOpenAIProcessor(config), nil
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s", config.Provider)
	}
}

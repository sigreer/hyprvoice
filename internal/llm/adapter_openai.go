package llm

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
)

// Prompts for different intervention levels
var levelPrompts = map[string]string{
	"minimal": `You are a speech-to-text proofreader. Make only minor corrections to the transcribed text.

Rules:
- Fix obvious typos and transcription errors
- Correct basic punctuation (periods, commas, question marks)
- Fix capitalization at sentence starts and for proper nouns
- Do NOT remove filler words or restructure sentences
- Do NOT change word choice or rephrase anything
- Preserve the speaker's exact wording as much as possible
- Output only the corrected text with no explanations`,

	"moderate": `You are a speech-to-text cleanup assistant. Clean up the transcribed speech while preserving the speaker's voice.

Rules:
- Remove filler words (um, uh, erm, like, you know, so, basically, etc.)
- Remove false starts, stutters, and repetitions
- Fix punctuation and capitalization
- Keep the original sentence structure where possible
- Maintain the speaker's word choices and expressions
- Do not add information not present in the original
- Output only the cleaned text with no explanations`,

	"thorough": `You are a speech-to-text editor. Rewrite the transcribed speech to be clear and coherent.

Rules:
- Remove all filler words, hesitations, and verbal tics
- Remove false starts, stutters, and repetitions
- Restructure run-on sentences for clarity
- Improve flow and readability while preserving meaning
- Combine fragmented thoughts into complete sentences
- Maintain the original intent and key information
- Keep a natural, conversational tone
- Do not add information not present in the original
- Output only the rewritten text with no explanations`,
}

func getPromptForLevel(level string, customPrompt string) string {
	if level == "custom" && customPrompt != "" {
		return customPrompt
	}
	if prompt, ok := levelPrompts[level]; ok {
		return prompt
	}
	// Default to moderate if level not found
	return levelPrompts["moderate"]
}

// OpenAIProcessor implements Processor using OpenAI's chat completion API
type OpenAIProcessor struct {
	client *openai.Client
	config Config
}

// NewOpenAIProcessor creates a new OpenAI processor
func NewOpenAIProcessor(config Config) *OpenAIProcessor {
	client := openai.NewClient(config.APIKey)
	return &OpenAIProcessor{
		client: client,
		config: config,
	}
}

// Process cleans up transcribed text using OpenAI's chat completion
func (p *OpenAIProcessor) Process(ctx context.Context, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return text, nil
	}

	// Create a timeout context for the LLM call
	llmCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	prompt := getPromptForLevel(p.config.Level, p.config.CustomPrompt)

	start := time.Now()
	resp, err := p.client.CreateChatCompletion(llmCtx, openai.ChatCompletionRequest{
		Model: p.config.Model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: prompt,
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: text,
			},
		},
		MaxTokens:   2048,
		Temperature: 0.3, // Low temperature for consistent output
	})
	duration := time.Since(start)

	if err != nil {
		log.Printf("llm-openai: API call failed after %v: %v", duration, err)
		return "", fmt.Errorf("openai chat completion: %w", err)
	}

	if len(resp.Choices) == 0 {
		log.Printf("llm-openai: no choices returned after %v", duration)
		return "", fmt.Errorf("openai returned no choices")
	}

	result := strings.TrimSpace(resp.Choices[0].Message.Content)
	log.Printf("llm-openai: processed in %v: %q -> %q", duration, text, result)
	return result, nil
}

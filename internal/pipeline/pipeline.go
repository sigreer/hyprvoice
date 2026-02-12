package pipeline

import (
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/leonardotrapani/hyprvoice/internal/config"
	"github.com/leonardotrapani/hyprvoice/internal/injection"
	"github.com/leonardotrapani/hyprvoice/internal/llm"
	"github.com/leonardotrapani/hyprvoice/internal/recording"
	"github.com/leonardotrapani/hyprvoice/internal/transcriber"
)

type Status string
type Action string

type PipelineError struct {
	Title   string
	Message string
	Err     error
}

const (
	Idle         Status = "idle"
	Recording    Status = "recording"
	Transcribing Status = "transcribing"
	Injecting    Status = "injecting"
)

const (
	Inject Action = "inject"
	Cancel Action = "cancel"
)

type Pipeline interface {
	Run(ctx context.Context)
	Stop()
	Status() Status
	GetActionCh() chan<- Action
	GetErrorCh() <-chan PipelineError
	SetWindowAddress(address string)
	GetWindowAddress() string
}

type pipeline struct {
	status        Status
	actionCh      chan Action
	errorCh       chan PipelineError
	config        *config.Config
	windowAddress string

	mu       sync.RWMutex
	wg       sync.WaitGroup
	cancel   context.CancelFunc
	stopOnce sync.Once

	running atomic.Bool
}

func New(cfg *config.Config) Pipeline {
	return &pipeline{
		actionCh: make(chan Action, 1),
		errorCh:  make(chan PipelineError, 10),
		config:   cfg,
	}
}
func (p *pipeline) Run(ctx context.Context) {
	if !p.running.CompareAndSwap(false, true) {
		log.Printf("Pipeline: Already running, ignoring Run() call")
		return
	}

	runCtx, cancel := context.WithTimeout(ctx, p.config.Recording.Timeout)
	p.setCancel(cancel)

	p.wg.Add(1)
	go p.run(runCtx)
}

func (p *pipeline) run(ctx context.Context) {
	defer func() {
		p.running.Store(false)
		p.setStatus(Idle)
		p.wg.Done()
	}()

	log.Printf("Pipeline: Starting recording")
	p.setStatus(Recording)

	recorder := recording.NewRecorder(p.config.ToRecordingConfig())
	frameCh, rErrCh, err := recorder.Start(ctx)

	if err != nil {
		log.Printf("Pipeline: Recording error: %v", err)
		p.sendError("Recording Error", "Failed to start recording", err)
		return
	}

	defer recorder.Stop()

	t, err := transcriber.NewTranscriber(p.config.ToTranscriberConfig())
	if err != nil {
		log.Printf("Pipeline: Failed to create transcriber: %v", err)
		p.sendError("Transcription Error", "Failed to create transcriber", err)
		return
	}

	log.Printf("Pipeline: Starting transcriber")
	p.setStatus(Transcribing)

	tErrCh, err := t.Start(ctx, frameCh)
	if err != nil {
		log.Printf("Pipeline: Transcriber error: %v", err)
		p.sendError("Transcription Error", "Failed to start transcriber", err)
		return
	}

	defer func() {
		if stopErr := t.Stop(ctx); stopErr != nil {
			log.Printf("Pipeline: Error stopping transcriber: %v", stopErr)
			// Silently call an error now because on simple transcriber we just transcribe all audio when we stop, and might fail when force stop
			//p.sendError("Transcription Error", "Failed to stop transcriber cleanly", stopErr)
		}
	}()

	// Forward errors from component channels to unified pipeline error channel
	go func() {
		for err := range tErrCh {
			p.sendError("Transcription Error", "Transcription processing error", err)
		}
	}()

	go func() {
		for err := range rErrCh {
			p.sendError("Recording Error", "Recording stream error", err)
		}
	}()

	for {
		select {
		case <-frameCh:

		case action := <-p.actionCh:
			switch action {
			case Inject:
				p.handleInjectAction(ctx, recorder, t)
				return
			}

		case <-ctx.Done():
			return
		}
	}
}

func (p *pipeline) Status() Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status
}

func (p *pipeline) setStatus(status Status) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status = status
}

func (p *pipeline) setCancel(cancel context.CancelFunc) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancel = cancel
}

func (p *pipeline) getCancel() context.CancelFunc {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cancel
}

func (p *pipeline) GetActionCh() chan<- Action {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.actionCh
}

func (p *pipeline) GetErrorCh() <-chan PipelineError {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.errorCh
}

func (p *pipeline) sendError(title, message string, err error) {
	pipelineErr := PipelineError{
		Title:   title,
		Message: message,
		Err:     err,
	}

	select {
	case p.errorCh <- pipelineErr:
	default:
		log.Printf("Pipeline: Error channel full, dropping error: %s", message)
	}
}

func (p *pipeline) handleInjectAction(ctx context.Context, recorder *recording.Recorder, t transcriber.Transcriber) {
	status := p.Status()

	if status != Transcribing {
		log.Printf("Pipeline: Inject action received, but not in transcribing state, ignoring")
		return
	}

	log.Printf("Pipeline: Inject action received, stopping recording and finalizing transcription")
	p.setStatus(Injecting)

	recorder.Stop()

	if err := t.Stop(ctx); err != nil {
		p.sendError("Transcription Error", "Failed to stop transcriber during injection", err)
		return
	}

	transcriptionText, err := t.GetFinalTranscription()
	if err != nil {
		p.sendError("Transcription Error", "Failed to retrieve transcription", err)
		return
	}
	log.Printf("Pipeline: Raw transcription text: %s", transcriptionText)

	// Strip wake phrase (fast, deterministic, works in all modes)
	if p.config.Processing.WakePhrase != "" && transcriptionText != "" {
		stripped := stripWakePhrase(transcriptionText, p.config.Processing.WakePhrase)
		if stripped != transcriptionText {
			log.Printf("Pipeline: Stripped wake phrase %q: %q -> %q", p.config.Processing.WakePhrase, transcriptionText, stripped)
			transcriptionText = stripped
		}
	}

	// LLM post-processing if enabled
	if p.config.Processing.Mode == "llm" && transcriptionText != "" {
		log.Printf("Pipeline: Processing with LLM...")
		processor, llmErr := llm.NewProcessor(p.config.ToLLMConfig())
		if llmErr != nil {
			log.Printf("Pipeline: Failed to create LLM processor, using raw: %v", llmErr)
		} else {
			processedText, procErr := processor.Process(ctx, transcriptionText)
			if procErr != nil {
				log.Printf("Pipeline: LLM processing failed, using raw: %v", procErr)
			} else {
				log.Printf("Pipeline: LLM cleaned text: %s", processedText)
				transcriptionText = processedText
			}
		}
	}

	log.Printf("Pipeline: Final text for injection: %s", transcriptionText)

	injector := injection.NewInjector(p.config.ToInjectionConfig())

	windowAddress := p.GetWindowAddress()
	if err := injector.Inject(ctx, transcriptionText, windowAddress); err != nil {
		p.sendError("Injection Error", "Failed to inject text", err)
	} else {
		log.Printf("Pipeline: Text injection completed successfully")
	}

	p.setStatus(Idle)
}

func (p *pipeline) Stop() {
	p.stopOnce.Do(func() {
		cancel := p.getCancel()
		if cancel != nil {
			cancel()
		}
	})
	p.wg.Wait()
}

func (p *pipeline) SetWindowAddress(address string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.windowAddress = address
}

func (p *pipeline) GetWindowAddress() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.windowAddress
}

// stripWakePhrase removes the wake phrase from the end of transcription text.
// Uses fuzzy matching (Levenshtein distance) to handle Whisper mistranscriptions
// like "hi Jarvis" or "hey Jarvis," when the wake phrase is "hey jarvis".
func stripWakePhrase(text, phrase string) string {
	trimmed := strings.TrimRight(text, " ")
	if trimmed == "" {
		return text
	}

	phraseWords := strings.Fields(phrase)
	n := len(phraseWords)
	if n == 0 {
		return text
	}

	// Find the byte position where the last N words start
	cutPos, found := findLastNWordsPos(trimmed, n)
	if !found {
		return text
	}

	suffix := trimmed[cutPos:]

	// Normalize both to lowercase letters only for comparison
	normSuffix := normLetters(suffix)
	normPhrase := normLetters(phrase)

	if normPhrase == "" {
		return text
	}

	dist := levenshtein(normSuffix, normPhrase)
	maxLen := max(len(normSuffix), len(normPhrase))

	// Allow up to 40% edit distance — catches "hi jarvis" vs "hey jarvis" (22%)
	// but rejects genuinely different phrases like "hey guys" (67%)
	if float64(dist)/float64(maxLen) > 0.4 {
		return text
	}

	result := strings.TrimRight(trimmed[:cutPos], " ,.\t-")
	if result == "" {
		return ""
	}
	return result
}

// findLastNWordsPos returns the byte position where the last n words start in text.
func findLastNWordsPos(text string, n int) (int, bool) {
	wordCount := 0
	i := len(text) - 1

	for i >= 0 && wordCount < n {
		// Skip trailing non-letter/digit characters (punctuation, spaces)
		for i >= 0 && !isWordChar(text[i]) {
			i--
		}
		if i < 0 {
			break
		}
		// Skip the word itself
		for i >= 0 && isWordChar(text[i]) {
			i--
		}
		wordCount++
	}

	if wordCount < n {
		if wordCount == 0 {
			return 0, false
		}
		return 0, true
	}
	return i + 1, true
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b == '\''
}

// normLetters returns only lowercase ASCII letters from s.
func normLetters(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}

	return prev[len(b)]
}

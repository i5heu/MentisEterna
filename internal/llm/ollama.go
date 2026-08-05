package llm

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/i5heu/MentisEterna/internal/config"
)

// Embedder defines the interface for generating text embeddings.
// This allows mocking in tests without requiring a running LocalAI instance.
type Embedder interface {
	GenerateEmbedding(text string) ([]float64, error)
}

// Generator defines the interface for generating text via an LLM.
// This allows mocking in tests without requiring a running LocalAI instance.
type Generator interface {
	GenerateTitle(text string) (string, error)
}

// AutoTagger suggests normalized tags for a note, choosing from existing tags
// when possible and proposing new ones only when useful.
type AutoTagger interface {
	SuggestTags(input AutoTagSuggestionInput) (AutoTagSuggestion, error)
}

// AutoTagSuggestionInput contains the note content and the current global tag
// vocabulary used to steer tag selection.
type AutoTagSuggestionInput struct {
	Title        string
	Body         string
	ExistingTags []string
	CurrentTags  []string
	MaxExisting  int
	MaxNew       int
	MaxTotal     int
}

// AutoTagSuggestion is the structured JSON shape expected back from the chat
// model.
type AutoTagSuggestion struct {
	ExistingTags []string `json:"existing_tags"`
	NewTags      []string `json:"new_tags"`
}

// SubTaskGenerator breaks a task description into a checklist of actionable
// subtasks to append to what is already planned.
type SubTaskGenerator interface {
	GenerateSubTasks(input SubTaskGenerationInput) (SubTaskSuggestion, error)
}

// SubTaskGenerationInput contains the task context used to plan subtasks.
type SubTaskGenerationInput struct {
	Title            string
	Description      string
	Body             string
	ExistingSubtasks []string
	MaxSubtasks      int
}

// SubTaskSuggestion is the strict JSON shape expected back from the chat model.
type SubTaskSuggestion struct {
	Subtasks []SubTaskItem `json:"subtasks"`
}

// SubTaskItem is a single generated subtask.
type SubTaskItem struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// --- Shared HTTP client & base URL helpers ---

// newLLMHTTPClient returns an *http.Client configured for LLM backend requests.
// When LOCALAI_TLS_INSECURE is a non-empty truthy string (e.g. "1", "true", "yes"),
// TLS certificate verification is skipped — useful for self-signed certificates
// on internal LAN addresses.
func newLLMHTTPClient() *http.Client {
	if config.Get().LLM.TLSInsecure {
		return &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
	}
	return &http.Client{}
}

// llmBaseURL returns the LocalAI base URL, configurable via config.toml
// ([llm] base_url; default: http://localhost:8080).
func llmBaseURL() string {
	if u := config.Get().LLM.BaseURL; u != "" {
		return u
	}
	return "http://localhost:8080"
}

// EmbeddingClient communicates with a LocalAI instance to generate embeddings
// via the OpenAI-compatible /v1/embeddings endpoint.
type EmbeddingClient struct {
	BaseURL string
	Model   string
	http    *http.Client
}

// NewEmbeddingClient creates a client with sensible defaults. The base URL and
// model are read from config.toml ([llm] base_url and embedding_model).
func NewEmbeddingClient() *EmbeddingClient {
	return &EmbeddingClient{
		BaseURL: llmBaseURL(),
		Model:   config.Get().LLM.EmbeddingModel,
		http:    newLLMHTTPClient(),
	}
}

// ChatClient communicates with a LocalAI instance for text generation
// (e.g., auto-generating note titles) via the OpenAI-compatible
// /v1/chat/completions endpoint.
type ChatClient struct {
	BaseURL string
	Model   string
	http    *http.Client
}

// NewChatClient creates a chat client for the given base URL and model.
func NewChatClient(baseURL, model string) *ChatClient {
	return &ChatClient{
		BaseURL: baseURL,
		Model:   model,
		http:    newLLMHTTPClient(),
	}
}

// OpenAI-compatible embedding request/response types.
type embeddingRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

// GenerateEmbedding hits the LocalAI /v1/embeddings endpoint (OpenAI-compatible)
// and returns a slice of float64 values representing the sentence embedding.
func (c *EmbeddingClient) GenerateEmbedding(text string) ([]float64, error) {
	reqBody := embeddingRequest{
		Model: c.Model,
		Input: text,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := c.BaseURL + "/v1/embeddings"
	resp, err := c.http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("localai request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("localai returned %d: %s", resp.StatusCode, string(body))
	}

	var er embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(er.Data) == 0 {
		return nil, fmt.Errorf("localai returned no embedding data")
	}

	return er.Data[0].Embedding, nil
}

// --- Chat / Generation ---

// OpenAI-compatible chat completion request/response types.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

func (c *ChatClient) complete(messages []chatMessage) (string, error) {
	reqBody := chatCompletionRequest{
		Model:    c.Model,
		Messages: messages,
		Stream:   false,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := c.BaseURL + "/v1/chat/completions"
	resp, err := c.http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("localai request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("localai returned %d: %s", resp.StatusCode, string(body))
	}

	var cr chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("localai returned no choices")
	}

	return cr.Choices[0].Message.Content, nil
}

// GenerateTitle asks the LLM to produce a short, concise title given a note's
// text content. It uses the LocalAI /v1/chat/completions endpoint (OpenAI-compatible).
func (c *ChatClient) GenerateTitle(text string) (string, error) {
	systemPrompt := `You are a highly constrained, automated backend microservice responsible for generating note titles. Your sole function is to receive raw note content and output a single, strictly formatted text string.

CRITICAL RULES:
1. MAXIMUM LENGTH: The output must not exceed 30 characters.
2. ALLOWED CHARACTERS: Strictly limited to alphanumeric characters, hyphens, spaces and underscores "[a-zA-Z0-9_-]". Absolutely NO emojis, and NO punctuation.
3. WORD SEPARATION: Because spaces are forbidden, you must use kebab-case (e.g., my-new-note) or snake_case (e.g., my_new_note) to separate words.
4. CONTENT EXTRACTION: Identify the core subject, action, or entity. Discard filler words (a, the, and).
5. FALLBACK: If the input is empty, completely unreadable, or lacks clear meaning, output exactly: Untitled
6. ZERO-SHOT OUTPUT: You must output ONLY the final string. NO markdown code blocks (do not use '''), NO quotation marks, NO preamble ("Here is the title:"), and NO conversational text.

EXAMPLES:
Input: "Need to remember to buy milk, eggs, and bread from the store tomorrow."
Output: grocery-list

Input: "Meeting with the design team regarding the new UI wireframes for the mobile app."
Output: design-team-ui-wireframes

Input: "12345 67890"
Output: 12345-67890

Input: ""
Output: Untitled

INPUT TO PROCESS:
[Insert User Note Content Here]`

	return c.complete([]chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: text},
	})
}

// SuggestTags asks the chat model to choose relevant existing tags and propose
// new tags in a strict JSON object.
func (c *ChatClient) SuggestTags(input AutoTagSuggestionInput) (AutoTagSuggestion, error) {
	if input.MaxExisting <= 0 {
		input.MaxExisting = 8
	}
	if input.MaxNew <= 0 {
		input.MaxNew = 4
	}
	if input.MaxTotal <= 0 {
		input.MaxTotal = 10
	}

	systemPrompt := fmt.Sprintf(`You are a backend tagging microservice.

Your task: read a note title/body and output exactly one JSON object with this shape:
{"existing_tags":["..."],"new_tags":["..."]}

Rules:
- Output JSON only. No markdown, no code fences, no commentary.
- Tags must be lowercase.
- Tags must be short, reusable labels, usually 1-3 words.
- Prefer existing tags whenever they fit.
- Use new_tags only for clearly useful new concepts not already covered.
- Do not repeat a tag across existing_tags and new_tags.
- Do not include any tag that already exists on the note.
- Do not invent overly specific, temporary, or sentence-like tags.
- Return at most %d existing tags, at most %d new tags, and at most %d tags total.
- If no good tags apply, return empty arrays.
`, input.MaxExisting, input.MaxNew, input.MaxTotal)

	userPayload := map[string]any{
		"title":         input.Title,
		"body":          input.Body,
		"existing_tags": input.ExistingTags,
		"current_tags":  input.CurrentTags,
	}
	userJSON, err := json.Marshal(userPayload)
	if err != nil {
		return AutoTagSuggestion{}, fmt.Errorf("marshal auto-tag input: %w", err)
	}

	raw, err := c.complete([]chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: string(userJSON)},
	})
	if err != nil {
		return AutoTagSuggestion{}, err
	}

	var out AutoTagSuggestion
	if err := unmarshalLooseJSONObject(raw, &out); err != nil {
		return AutoTagSuggestion{}, fmt.Errorf("decode auto-tag response: %w", err)
	}
	return out, nil
}

// GenerateSubTasks asks the chat model to plan a checklist of actionable
// subtasks for a task, based on its title, description, body, and any
// subtasks that already exist.
func (c *ChatClient) GenerateSubTasks(input SubTaskGenerationInput) (SubTaskSuggestion, error) {
	if input.MaxSubtasks <= 0 {
		input.MaxSubtasks = 10
	}
	systemPrompt := fmt.Sprintf(`You are an intuitive human-centric task planner and productivity assistant operating as an automated backend microservice.

	### TASK GOAL
	Deconstruct the provided task (defined by its title, description, and body) into a set of sequence-dependent, ultra-low-friction micro-actions. The goal is to completely bypass task initiation paralysis, externalize working memory, and provide an effortless runway for execution. Ensure zero overlap with any pre-existing subtasks.

	### INPUT FORMAT
	You will receive the task details structured as follows:
	- Task Title: {title}
	- Task Description: {description}
	- Task Body: {body}
	- Max Subtasks Allowed: {max_subtasks}
	- Existing Subtasks: {existing_subtasks}

	### HUMAN-CENTRIC TASK DESIGN PRINCIPLES
	1. WARM STARTS (ZERO-FRICTION INITIATION): The absolute first subtask MUST be a trivially small physical action (e.g., "Open the document," "Stand up," "Get one pen"). Make the initial hurdle so microscopic that it bypasses all cognitive resistance.
	2. BREADCRUMBING (WORKING MEMORY OFFLOAD): Use the description field to leave a "breadcrumb" for the user's future self. Provide the exact, isolated context needed for that specific step so the user never has to hold complex information in their head or re-evaluate a prior decision.
	3. DECISION ELIMINATION: Never provide open-ended options or abstract choices. Pre-decide the sequence explicitly so the user only has to execute, not analyze.
	4. NEXT PHYSICAL ACTION: Frame every step as a concrete physical movement rather than an abstract milestone (e.g., "Put the dark clothes in the machine" rather than "Sort laundry").
	5. SINGLE HORIZON FOCUS: Keep the immediate steps focused only on the next 10 to 15 minutes of action to prevent overwhelming the user's planning centers.

	### GENERATION RULES
	1. ACTIONABLE STEPS: Each subtask must represent a single, self-contained, physical step.
	2. IMPERATIVE & HUMAN LABELS: "label" must be a concise, warm, natural phrase starting with an action verb (e.g., "Gather the materials," "Check the calendar"). Do NOT add numbers, bullet points, prefixes, or markdown formatting to labels.
	3. CONCISE DESCRIPTIONS: "description" is optional. If provided, it must be exactly one short, encouraging, or clarifying sentence acting as a context "breadcrumb." Omit the key or set to null if unneeded.
	4. QUANTITY LIMIT: Generate at most {max_subtasks} subtasks.
	5. DEDUPLICATION: Do NOT duplicate, rephrase, or overlap with any steps already listed in "Existing Subtasks".
	6. EMPTY / INVALID CONTEXT: If the input context is vague, incomplete, missing, or if existing subtasks already fully cover the task, return an empty array for "subtasks".

	### OUTPUT FORMAT REQUIREMENTS
	- Output EXACTLY one valid JSON object adhering strictly to this JSON schema:
	{
  "subtasks": [
    {
      "label": "Short natural action step",
      "description": "Optional single sentence clarifying detail."
    }
  ]
	}
	- CRITICAL: Output raw JSON ONLY.
	- Do NOT wrap the JSON in markdown code blocks (e.g., forbidden: '''json ...''').
	- Do NOT include any preamble, intro, outro, explanation, or commentary.
`)

	userPayload := map[string]any{
		"title":             input.Title,
		"description":       input.Description,
		"body":              input.Body,
		"existing_subtasks": input.ExistingSubtasks,
	}
	userJSON, err := json.Marshal(userPayload)
	if err != nil {
		return SubTaskSuggestion{}, fmt.Errorf("marshal subtask input: %w", err)
	}

	raw, err := c.complete([]chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: string(userJSON)},
	})
	if err != nil {
		return SubTaskSuggestion{}, err
	}

	var out SubTaskSuggestion
	if err := unmarshalLooseJSONObject(raw, &out); err != nil {
		return SubTaskSuggestion{}, fmt.Errorf("decode subtask response: %w", err)
	}
	return out, nil
}

func unmarshalLooseJSONObject(raw string, dst any) error {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)

	if err := json.Unmarshal([]byte(trimmed), dst); err == nil {
		return nil
	}

	start := strings.IndexByte(trimmed, '{')
	end := strings.LastIndexByte(trimmed, '}')
	if start >= 0 && end > start {
		return json.Unmarshal([]byte(trimmed[start:end+1]), dst)
	}
	return fmt.Errorf("no JSON object found in %q", trimmed)
}

// EmbeddingToJSON marshals a float64 slice to a VSS-compatible JSON array
// string like "[0.1,0.2,...]".
func EmbeddingToJSON(vec []float64) string {
	b, _ := json.Marshal(vec)
	return string(b)
}

// CombineTitleBody returns a single input string for the embedding model.
func CombineTitleBody(title, body string) string {
	if body == "" {
		return title
	}
	return title + "\n" + body
}

// MaxEmbeddingChars returns the rune limit for embedding input, read from
// config (llm.embedding_max_chars); defaults to 16K.
var MaxEmbeddingChars = func() int { return config.Get().LLM.EmbeddingMaxChars }

// TruncateForEmbedding ensures text does not exceed the embedding model's
// context window. It trims to MaxEmbeddingChars runes, preserving valid UTF-8
// and trying to break on a whitespace boundary.
func TruncateForEmbedding(text string) string {
	if utf8.RuneCountInString(text) <= MaxEmbeddingChars() {
		return text
	}
	runes := []rune(text)
	if len(runes) <= MaxEmbeddingChars() {
		return text
	}
	truncated := string(runes[:MaxEmbeddingChars()])
	if idx := strings.LastIndexAny(truncated, " \t\n\r"); idx > MaxEmbeddingChars()/2 {
		return strings.TrimRight(truncated[:idx], " \t\n\r")
	}
	return strings.TrimRight(truncated, " \t\n\r")
}

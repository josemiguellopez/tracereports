package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

// Config selects the AI provider. It comes from the environment (.env) and can be overridden
// from the Settings screen.
type Config struct {
	Provider string `json:"provider"` // gemini | anthropic | openai | openai_compatible | ollama | "" (off)
	Model    string `json:"model"`
	APIKey   string `json:"-"`
	BaseURL  string `json:"base_url"`
}

// ProviderInfo describes a supported provider for the Settings screen.
type ProviderInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	DefaultModel   string `json:"default_model"`
	DefaultBaseURL string `json:"default_base_url"`
	NeedsKey       bool   `json:"needs_key"`
	NeedsBaseURL   bool   `json:"needs_base_url"`
	KeyURL         string `json:"key_url,omitempty"` // where to get an API key
	Note           string `json:"note"`
}

// Providers lists the supported providers, in the order the UI shows them.
var Providers = []ProviderInfo{
	// alias de Google: apunta siempre al flash-lite vigente, así no queda obsoleto (gemini-2.5-flash ya no se
	// habilita a cuentas nuevas) y tiene más cuota gratuita que los flash completos
	{ID: "gemini", Name: "Google Gemini", DefaultModel: "gemini-flash-lite-latest", DefaultBaseURL: "https://generativelanguage.googleapis.com",
		NeedsKey: true, KeyURL: "https://aistudio.google.com/apikey", Note: "Tiene capa gratuita."},
	{ID: "anthropic", Name: "Anthropic Claude", DefaultModel: "claude-opus-5-5", DefaultBaseURL: "https://api.anthropic.com",
		NeedsKey: true, KeyURL: "https://platform.claude.com/settings/keys", Note: "Diagnósticos más precisos; de pago por uso."},
	{ID: "openai", Name: "OpenAI", DefaultModel: "gpt-5-mini", DefaultBaseURL: "https://api.openai.com/v1",
		NeedsKey: true, KeyURL: "https://platform.openai.com/api-keys", Note: "De pago por uso."},
	{ID: "openai_compatible", Name: "Compatible con OpenAI", DefaultModel: "", DefaultBaseURL: "",
		NeedsKey: false, NeedsBaseURL: true, Note: "Groq, OpenRouter, DeepSeek, Mistral, LM Studio, vLLM… cualquier API /v1/chat/completions."},
	{ID: "ollama", Name: "Ollama (local)", DefaultModel: "llama3.1", DefaultBaseURL: "http://localhost:11434",
		Note: "Modelos en tu propia máquina: los datos de los tests no salen de tu red. Sin API key."},
}

// ProviderByID returns the provider description, or nil.
func ProviderByID(id string) *ProviderInfo {
	for i := range Providers {
		if Providers[i].ID == id {
			return &Providers[i]
		}
	}
	return nil
}

// withDefaults fills the model and base URL with the provider's defaults.
func (c Config) withDefaults() Config {
	c.Provider = strings.ToLower(strings.TrimSpace(c.Provider))
	c.Model = strings.TrimSpace(c.Model)
	c.APIKey = strings.TrimSpace(c.APIKey)
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if p := ProviderByID(c.Provider); p != nil {
		if c.Model == "" {
			c.Model = p.DefaultModel
		}
		if c.BaseURL == "" {
			c.BaseURL = p.DefaultBaseURL
		}
	}
	return c
}

// Validate reports a configuration that cannot work (unknown provider, missing key or URL).
func (c Config) Validate() error {
	c = c.withDefaults()
	if c.Provider == "" {
		return nil // IA desactivada
	}
	p := ProviderByID(c.Provider)
	switch {
	case p == nil:
		return fmt.Errorf("proveedor de IA desconocido: %q", c.Provider)
	case p.NeedsKey && c.APIKey == "":
		return fmt.Errorf("%s necesita una API key", p.Name)
	case c.BaseURL == "":
		return fmt.Errorf("%s necesita la URL base de la API", p.Name)
	case c.Model == "":
		return errors.New("falta el modelo")
	}
	return nil
}

// ConfigFromEnv reads the provider from the environment. AI_PROVIDER / AI_MODEL / AI_API_KEY /
// AI_BASE_URL are generic; without AI_PROVIDER the provider is inferred from the first
// provider-specific key found (GEMINI_API_KEY, ANTHROPIC_API_KEY, OPENAI_API_KEY, OLLAMA_HOST),
// so existing GEMINI_* setups keep working.
func ConfigFromEnv() Config {
	env := func(keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimSpace(os.Getenv(k)); v != "" {
				return v
			}
		}
		return ""
	}
	c := Config{Provider: env("AI_PROVIDER")}
	if c.Provider == "" {
		switch {
		case env("GEMINI_API_KEY") != "":
			c.Provider = "gemini"
		case env("ANTHROPIC_API_KEY") != "":
			c.Provider = "anthropic"
		case env("OPENAI_API_KEY") != "":
			c.Provider = "openai"
		case env("OLLAMA_HOST") != "":
			c.Provider = "ollama"
		}
	}
	switch strings.ToLower(c.Provider) {
	case "gemini":
		c.Model, c.APIKey, c.BaseURL = env("AI_MODEL", "GEMINI_MODEL"), env("AI_API_KEY", "GEMINI_API_KEY"), env("AI_BASE_URL", "GEMINI_BASE_URL")
	case "anthropic":
		c.Model, c.APIKey, c.BaseURL = env("AI_MODEL", "ANTHROPIC_MODEL"), env("AI_API_KEY", "ANTHROPIC_API_KEY"), env("AI_BASE_URL", "ANTHROPIC_BASE_URL")
	case "openai":
		c.Model, c.APIKey, c.BaseURL = env("AI_MODEL", "OPENAI_MODEL"), env("AI_API_KEY", "OPENAI_API_KEY"), env("AI_BASE_URL", "OPENAI_BASE_URL")
	case "ollama":
		c.Model, c.BaseURL = env("AI_MODEL", "OLLAMA_MODEL"), env("AI_BASE_URL", "OLLAMA_HOST")
		if c.BaseURL != "" && !strings.Contains(c.BaseURL, "://") {
			c.BaseURL = "http://" + c.BaseURL // OLLAMA_HOST suele ser "localhost:11434"
		}
	default:
		c.Model, c.APIKey, c.BaseURL = env("AI_MODEL"), env("AI_API_KEY"), env("AI_BASE_URL")
	}
	return c.withDefaults()
}

// EnvKey returns the API key the environment has for provider (AI_API_KEY when AI_PROVIDER
// names it, or the provider's own variable). Lets the Settings screen switch provider without
// retyping a key that already lives in the .env.
func EnvKey(provider string) string {
	if strings.EqualFold(os.Getenv("AI_PROVIDER"), provider) {
		if k := strings.TrimSpace(os.Getenv("AI_API_KEY")); k != "" {
			return k
		}
	}
	switch provider {
	case "gemini":
		return strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	case "anthropic":
		return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	case "openai":
		return strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	return ""
}

// ---- llamada genérica ----

// httpStatusError is a non-2xx answer from a provider; 429 and 5xx are retried.
type httpStatusError struct {
	provider string
	code     int
	body     string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("%s HTTP %d: %s", e.provider, e.code, e.body)
}

var (
	dailyQuota = regexp.MustCompile(`(?i)per ?day|retry in \d+h`)
	// sin saldo: OpenAI "insufficient_quota / no credits remaining", Anthropic "credit balance is too low"
	noCredits = regexp.MustCompile(`(?i)no credits|credit balance|insufficient_quota|billing`)
)

// retryable: rate limits per minute and server errors; never a used-up daily quota (it would only
// make the diagnosis wait for nothing).
func (e *httpStatusError) retryable() bool {
	if e.code == http.StatusTooManyRequests {
		return !dailyQuota.MatchString(e.body) && !noCredits.MatchString(e.body)
	}
	return e.code >= 500
}

// Hint classifies a provider error so the UI can explain it and offer a fix:
// "model_unavailable" (404), "no_credits" / "quota_daily" / "rate_limit" (429 or 402/400 without
// balance), "overloaded" (503) or "".
// suggest is a model worth trying instead ("" when there is no better default).
func Hint(err error, c Config) (code, suggest string) {
	var se *httpStatusError
	if !errors.As(err, &se) {
		return "", ""
	}
	c = c.withDefaults()
	if p := ProviderByID(c.Provider); p != nil && p.DefaultModel != c.Model {
		suggest = p.DefaultModel
	}
	switch {
	case noCredits.MatchString(se.body) && se.code != http.StatusNotFound:
		return "no_credits", "" // otro modelo del mismo proveedor tampoco tendría saldo
	case se.code == http.StatusNotFound:
		return "model_unavailable", suggest
	case se.code == http.StatusTooManyRequests && dailyQuota.MatchString(se.body):
		return "quota_daily", suggest
	case se.code == http.StatusTooManyRequests:
		return "rate_limit", ""
	case se.code == http.StatusServiceUnavailable || se.code == 529:
		return "overloaded", suggest
	}
	return "", ""
}

// HintText is Hint as a sentence, for errors saved next to a test (the UI shows them as-is).
func HintText(err error, c Config) string {
	code, suggest := Hint(err, c)
	try := ""
	if suggest != "" {
		try = " Prueba con " + suggest + "."
	}
	switch code {
	case "model_unavailable":
		return " → El modelo no está disponible para esta cuenta." + try
	case "quota_daily":
		return " → Se agotó la cuota diaria de este modelo." + try
	case "no_credits":
		return " → La cuenta del proveedor no tiene saldo: agrega créditos o cambia de proveedor."
	case "overloaded":
		return " → El modelo está saturado en este momento." + try
	}
	return ""
}

// call asks the configured provider for a JSON answer matching schema (standard JSON Schema)
// and returns the JSON text.
func call(ctx context.Context, client *http.Client, c Config, prompt string, schema map[string]any) (string, error) {
	var (
		text string
		err  error
	)
	switch c.Provider {
	case "gemini":
		text, err = callGemini(ctx, client, c, prompt, schema)
	case "anthropic":
		text, err = callAnthropic(ctx, client, c, prompt, schema)
	case "openai", "openai_compatible":
		text, err = callOpenAI(ctx, client, c, prompt, schema)
	case "ollama":
		text, err = callOllama(ctx, client, c, prompt, schema)
	default:
		return "", fmt.Errorf("proveedor de IA desconocido: %q", c.Provider)
	}
	if err != nil {
		// sin la key de esta solicitud: el error se registra, se guarda y se muestra (scrub.go)
		return "", scrubError(err, c.APIKey)
	}
	// el texto de una respuesta HTTP 200 tampoco: termina en errores locales ("no es JSON": recortado),
	// diagnósticos, escalados y resúmenes. Se quita entero, antes de cualquier recorte
	return cleanJSON(scrubKey(text, c.APIKey)), nil
}

// cleanJSON strips the ```json fences some models add even when asked for raw JSON.
func cleanJSON(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func postJSON(ctx context.Context, client *http.Client, provider, url string, headers map[string]string, body any) ([]byte, error) {
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &httpStatusError{provider: provider, code: resp.StatusCode, body: apiErrorMessage(raw)}
	}
	return raw, nil
}

// apiErrorMessage extracts error.message (Google, OpenAI) or error (Ollama) from an error body.
func apiErrorMessage(raw []byte) string {
	var e struct {
		Error json.RawMessage `json:"error"`
	}
	msg := string(raw)
	if json.Unmarshal(raw, &e) == nil && len(e.Error) > 0 {
		var obj struct {
			Message string `json:"message"`
		}
		var str string
		if json.Unmarshal(e.Error, &obj) == nil && obj.Message != "" {
			msg = obj.Message
		} else if json.Unmarshal(e.Error, &str) == nil && str != "" {
			msg = str
		}
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return strings.TrimSpace(msg)
}

// ---- esquemas: cada proveedor habla un dialecto distinto de JSON Schema ----

// geminiSchema converts standard JSON Schema to Gemini's OpenAPI subset (UPPERCASE types,
// no additionalProperties).
func geminiSchema(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range t {
			switch k {
			case "additionalProperties":
			case "type":
				if s, ok := val.(string); ok {
					out[k] = strings.ToUpper(s)
				}
			default:
				out[k] = geminiSchema(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = geminiSchema(t[i])
		}
		return out
	}
	return v
}

// closedSchema adds additionalProperties:false to every object (required by Claude's and
// OpenAI's structured outputs).
func closedSchema(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range t {
			out[k] = closedSchema(val)
		}
		if out["type"] == "object" {
			out["additionalProperties"] = false
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = closedSchema(t[i])
		}
		return out
	}
	return v
}

// ---- Google Gemini ----

type geminiRequest struct {
	Contents         []geminiContent `json:"contents"`
	GenerationConfig map[string]any  `json:"generationConfig"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

func callGemini(ctx context.Context, client *http.Client, c Config, prompt string, schema map[string]any) (string, error) {
	raw, err := postJSON(ctx, client, "gemini", fmt.Sprintf("%s/v1beta/models/%s:generateContent", c.BaseURL, c.Model),
		map[string]string{"x-goog-api-key": c.APIKey},
		geminiRequest{
			Contents: []geminiContent{{Role: "user", Parts: []geminiPart{{Text: prompt}}}},
			GenerationConfig: map[string]any{
				"temperature":      0.2,
				"responseMimeType": "application/json",
				"responseSchema":   geminiSchema(schema),
			},
		})
	if err != nil {
		return "", err
	}
	var gr struct {
		Candidates []struct {
			Content geminiContent `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &gr); err != nil {
		return "", fmt.Errorf("decode gemini response: %w", err)
	}
	if len(gr.Candidates) == 0 || len(gr.Candidates[0].Content.Parts) == 0 {
		return "", errors.New("gemini returned no candidates")
	}
	return gr.Candidates[0].Content.Parts[0].Text, nil
}

// ---- Anthropic Claude (SDK oficial) ----

// claudeFallbackModels accept the server-side "default" refusal fallback: if the model declines
// for policy reasons the API re-serves the request on a fallback model inside the same call.
var claudeFallbackModels = map[string]bool{
	"claude-fable-5-1": true, "claude-opus-5-5": true, "claude-opus-5": true, "claude-sonnet-5-5": true,
}

func callAnthropic(ctx context.Context, client *http.Client, c Config, prompt string, schema map[string]any) (string, error) {
	opts := []option.RequestOption{option.WithAPIKey(c.APIKey), option.WithHTTPClient(client)}
	if c.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(c.BaseURL))
	}
	cl := anthropic.NewClient(opts...)
	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(c.Model),
		MaxTokens: 16000,
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(prompt))},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Format: anthropic.BetaJSONOutputFormatParam{Schema: closedSchema(schema).(map[string]any)},
		},
	}
	if claudeFallbackModels[c.Model] {
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		params.Fallbacks = anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
	}
	msg, err := cl.Beta.Messages.New(ctx, params)
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			// el SDK ya reintentó 429 y 5xx: no reintentar de nuevo
			return "", fmt.Errorf("anthropic HTTP %d: %s", apiErr.StatusCode, apiErrorMessage([]byte(apiErr.RawJSON())))
		}
		return "", err
	}
	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return "", errors.New("Claude rechazó analizar este fallo (refusal)")
	case anthropic.BetaStopReasonMaxTokens:
		return "", errors.New("la respuesta de Claude quedó cortada (max_tokens)")
	}
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			return t.Text, nil
		}
	}
	return "", errors.New("Claude no devolvió texto")
}

// ---- OpenAI y APIs compatibles (Groq, OpenRouter, DeepSeek, LM Studio, vLLM…) ----

func callOpenAI(ctx context.Context, client *http.Client, c Config, prompt string, schema map[string]any) (string, error) {
	headers := map[string]string{}
	if c.APIKey != "" {
		headers["Authorization"] = "Bearer " + c.APIKey
	}
	body := map[string]any{
		"model":    c.Model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{
			"name": "result", "schema": closedSchema(schema),
		}},
	}
	raw, err := postJSON(ctx, client, c.Provider, c.BaseURL+"/chat/completions", headers, body)
	var se *httpStatusError
	if errors.As(err, &se) && se.code == http.StatusBadRequest {
		// Algunas APIs compatibles no soportan json_schema: JSON simple con el esquema en el prompt.
		schemaText, _ := json.Marshal(schema)
		body["messages"] = []map[string]string{{"role": "user", "content": prompt +
			"\n\nRespond with a single JSON object that matches this JSON Schema:\n" + string(schemaText)}}
		body["response_format"] = map[string]string{"type": "json_object"}
		raw, err = postJSON(ctx, client, c.Provider, c.BaseURL+"/chat/completions", headers, body)
	}
	if err != nil {
		return "", err
	}
	var or struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
				Refusal string `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &or); err != nil {
		return "", fmt.Errorf("decode %s response: %w", c.Provider, err)
	}
	if len(or.Choices) == 0 {
		return "", fmt.Errorf("%s no devolvió respuesta", c.Provider)
	}
	if r := or.Choices[0].Message.Refusal; r != "" {
		return "", fmt.Errorf("el modelo rechazó la solicitud: %s", r)
	}
	return or.Choices[0].Message.Content, nil
}

// ---- Ollama (local) ----

func callOllama(ctx context.Context, client *http.Client, c Config, prompt string, schema map[string]any) (string, error) {
	raw, err := postJSON(ctx, client, "ollama", c.BaseURL+"/api/chat", nil, map[string]any{
		"model":    c.Model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"stream":   false,
		"format":   schema,
		"options":  map[string]any{"temperature": 0.2},
	})
	if err != nil {
		var ne interface{ Timeout() bool }
		if errors.As(err, &ne) || strings.Contains(err.Error(), "connection refused") {
			return "", fmt.Errorf("no se pudo conectar con Ollama en %s (¿está corriendo `ollama serve` y descargaste el modelo con `ollama pull %s`?): %w", c.BaseURL, c.Model, err)
		}
		return "", err
	}
	var or struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &or); err != nil {
		return "", fmt.Errorf("decode ollama response: %w", err)
	}
	return or.Message.Content, nil
}

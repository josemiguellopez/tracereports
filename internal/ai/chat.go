package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// UsageChat: free questions to Trace Buddy (Settings → AI usage).
const UsageChat = "chat"

// ErrChatNeedsLLM: the local rules engine diagnoses but cannot hold a conversation.
var ErrChatNeedsLLM = errors.New("the chat needs a language model (the local rules engine cannot chat)")

// Limits of what one question carries to the model: the last turns of the conversation, each clipped.
const (
	MaxChatMessages = 8
	MaxChatChars    = 2000
)

// ChatMessage is one turn of the conversation with Trace Buddy.
type ChatMessage struct {
	Role string `json:"role"` // user | assistant
	Text string `json:"text"`
}

// CleanChat keeps what goes to the model within the limits: known roles, non-empty turns clipped to
// MaxChatChars and only the last MaxChatMessages. The last turn must be the user's question.
func CleanChat(msgs []ChatMessage) ([]ChatMessage, error) {
	var out []ChatMessage
	for _, m := range msgs {
		text := strings.TrimSpace(m.Text)
		if text == "" || (m.Role != "user" && m.Role != "assistant") {
			continue
		}
		out = append(out, ChatMessage{Role: m.Role, Text: clipRunes(text, MaxChatChars)})
	}
	if len(out) > MaxChatMessages {
		out = out[len(out)-MaxChatMessages:]
	}
	if len(out) == 0 || out[len(out)-1].Role != "user" {
		return nil, errors.New("the last message must be the user's question")
	}
	return out, nil
}

// ChatExtra is what the chat adds to the evidence of the run or test.
type ChatExtra struct {
	// Calls: the failed calls of the test being viewed, as cURL with secrets replaced (repro.Curl) and
	// the start of each response: enough to explain how to reproduce them.
	Calls string
	// Shown: what the UI attaches below the answer, built from the report and not by the model ("curl",
	// "mock"): the model refers to it instead of writing it again.
	Shown []string
}

// chatTools tells the model what TraceReports already offers, so it points there instead of guessing.
const chatTools = `What TraceReports offers for the test being viewed (point to it by name when it helps):
- Network tab: every call, with "Copy as cURL" (Postman imports a cURL: Import → Raw text, paste it).
- "Generate mock of the failed backend", under the test error: a stub with the real response for WireMock,
  a Playwright route or a Cypress intercept, to reproduce the failure without the backend.
- The Lab (Network → a call → Lab): resends the call to check whether it still fails.
- The AI view: my diagnosis of the run, and Re-analyze. Escalate: a summary for business, QA or dev.`

// Chat answers a free question in Trace Buddy, with the evidence of the run or test being viewed
// (f; nil when no run is open). Only with an LLM; the prompt goes through the redaction like every
// other call (generate), so neither the evidence nor what the user typed carries secrets.
func (a *Analyzer) Chat(ctx context.Context, f *Facts, scope, lang string, msgs []ChatMessage, extra ChatExtra) (string, error) {
	if !a.UsesLLM() {
		return "", ErrChatNeedsLLM
	}
	msgs, err := CleanChat(msgs)
	if err != nil {
		return "", err
	}
	language := "the language of the user's last message"
	switch lang {
	case "es":
		language = "Spanish"
	case "en":
		language = "English"
	}
	evidence := "No report is open: answer from general testing knowledge and how TraceReports works."
	if f != nil {
		evidence = f.promptBlock(scope)
	}
	if extra.Calls != "" {
		evidence += "\nFailed calls of this test as cURL (secrets replaced by $VARIABLES or ***):\n" + extra.Calls
	}
	shown := ""
	if len(extra.Shown) > 0 {
		shown = "Below your answer the interface shows, built from the report: " + strings.Join(extra.Shown, " and ") +
			". Refer to it (\"below you have…\") and explain how to use it; do not write it again.\n"
	}
	var conv strings.Builder
	for _, m := range msgs {
		who := "User"
		if m.Role == "assistant" {
			who = "Trace Buddy"
		}
		fmt.Fprintf(&conv, "%s: %s\n", who, m.Text)
	}
	prompt := fmt.Sprintf(`You are Trace Buddy, the QA assistant inside TraceReports, a server that collects automated test
results (steps, screenshots, network calls, console) and diagnoses failures. You help QA and test automation
engineers understand failed tests, decide what to check first and use TraceReports.

Answer in %s, in plain language and briefly: at most 6 sentences, or a short list. Base the answer on the
evidence below and on general testing knowledge; do not invent data that is not there. When the evidence
does not answer the question, say so and point to where to look in the report (Steps, Network, Console,
Replay, the AI view, Errors). Speak in first person, as Trace Buddy: the per-test triage is your own
diagnosis ("mi diagnóstico" in Spanish, "my diagnosis" in English); never say "the AI suggests". Do not
name the labels of the evidence (the user does not see them). In a follow-up, do not repeat what you already
said: add what is new. Only answer questions about testing, test automation, this report or TraceReports;
politely decline anything else. Never ask for or repeat passwords, tokens or API keys.

%s
%s
Evidence (%s):
%s
Conversation (answer the last message of the user):
%s`, language, chatTools, shown, scope, evidence, conv.String())
	text, err := a.generate(ctx, UsageChat, prompt, map[string]any{
		"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}, "required": []string{"answer"},
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Answer string `json:"answer"`
	}
	if json.Unmarshal([]byte(text), &out) != nil || strings.TrimSpace(out.Answer) == "" {
		return "", fmt.Errorf("the model answered, but not with the expected JSON: %.120s", text)
	}
	return strings.TrimSpace(out.Answer), nil
}

// clipRunes cuts s to n characters without splitting one (questions in Spanish carry accents).
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

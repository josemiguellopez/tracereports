package ai

import (
	"context"
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
	// Tooling: the test's tool and language, detected from the run and the error ("Selenium (Python)"),
	// so the model never asks which framework is used.
	Tooling string
	// Page: the page at the failure read from the DOM snapshot (what the failed selector matched, the
	// loading indicators and what covers the page, with their locators).
	Page string
	// Fallback: an answer built from the evidence, used when the model only repeats an earlier answer.
	Fallback string
}

// repeats tells whether an answer says again what an earlier answer of the conversation said (a small
// model stuck asking "which framework?" after it was answered).
func repeats(answer string, msgs []ChatMessage) bool {
	a := words(answer)
	if len(a) == 0 {
		return false
	}
	for _, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		b := words(m.Text)
		common := 0
		for w := range a {
			if b[w] {
				common++
			}
		}
		if union := len(a) + len(b) - common; union > 0 && float64(common)/float64(union) >= 0.8 {
			return true
		}
	}
	return false
}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127)
	}) {
		out[w] = true
	}
	return out
}

// chatTools tells the model what TraceReports already offers, so it points there instead of guessing.
const chatTools = `What TraceReports offers for the test being viewed (point to it by name when it helps):
- Network tab: every call, with "Copy as cURL" (Postman imports a cURL: Import → Raw text, paste it).
- "Generate mock of the failed backend", under the test error: a stub with the real response for WireMock,
  a Playwright route or a Cypress intercept, to reproduce the failure without the backend.
- The Lab (Network → a call → Lab): resends the call to check whether it still fails.
- The Inspector tab: the page at the moment of the failure rebuilt from the DOM snapshot, without
  reproducing the failure. Try an XPath or CSS on it, and right click an element → QA / Automation for a
  robust XPath and code for Selenium or Playwright. Point there for anything about the DOM or a selector.
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
	evidence := "No report is open. That is fine: answer questions about testing, selectors and test code directly from your own knowledge."
	if f != nil {
		evidence = fmt.Sprintf("Evidence of the %s being viewed (use it only when the question is about this failure):\n%s", scope, f.promptBlock(scope))
	}
	if extra.Tooling != "" {
		evidence += "\nThe test uses " + extra.Tooling + " (detected by TraceReports: do not ask which framework or language; write code for it).\n"
	}
	if extra.Page != "" {
		evidence += "\n" + extra.Page
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
	// las reglas van después de la conversación: los modelos locales chicos obedecen mejor lo último que
	// leen, y sin el ejemplo responden a un pedido de selector con un diagnóstico general
	prompt := fmt.Sprintf(`You are Trace Buddy, a senior QA Automation engineer inside TraceReports, a server that collects
automated test results (steps, screenshots, network calls, console) and diagnoses failures. You have years
of hands-on experience with Selenium, Playwright, Cypress, API testing, Page Objects and CI pipelines, and
you answer like a senior colleague: concrete, practical and with working code, explaining the trade-off when
there is one (robustness, speed, maintainability). You help QA and test automation engineers with failed
tests, selectors, waits, flaky tests, assertions and test code, and with using TraceReports.

%s
%s
%s

Conversation:
%s
How to answer the last message of the User:
- Answer in %s, exactly what it asks, briefly (at most 6 sentences or a short list, plus code if needed).
- When it asks to write, fix or improve a selector (XPath, CSS, Playwright or Selenium locator), a wait, an
  assertion or test code, do it: the new version on its own line first, then one or two sentences on why it
  is more robust. You do not need a report for that. Example:
  User: improve //button[text()='Save'] with contains, ignoring case
  Trace Buddy: //button[contains(translate(normalize-space(.), 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz'), 'save')]
  contains() accepts extra text around the label, normalize-space() ignores spaces and translate() turns it to lowercase.
- When it is about the failure being viewed, base the answer on the evidence; do not invent data. If the
  evidence does not answer it, say so and point to where to look (Steps, Network, Console, Replay, the AI
  view, Errors). Speak in first person: the per-test triage is your own diagnosis ("mi diagnóstico" in
  Spanish); never say "the AI suggests" and never suggest configuring an AI provider.
- When it asks for the DOM, an element, a loading indicator (skeleton, spinner) or a wait for this failure,
  answer from "Page at the moment of the failure": give the XPath or CSS listed there and an explicit wait in
  the test's tool (e.g. Selenium Python: WebDriverWait(driver, 15).until(EC.invisibility_of_element_located(
  (By.XPATH, "..."))) before the click), and mention the Inspector tab. Never send them to Network for the DOM.
- Do not greet again after your first answer. Never ask something the user or the evidence already answered.
- Never repeat an earlier answer; if the user says you did not answer, answer the question now.
- Only testing, test automation, this report or TraceReports; politely decline anything else. Never ask
  for or repeat passwords, tokens, API keys or the contents of .env files.`, chatTools, shown, evidence, conv.String(), language)
	// texto libre, no JSON: un modelo local chico no escapa las comillas del código que escribe y la
	// respuesta queda cortada en la primera ("By.XPATH, "//...)
	text, err := a.generate(ctx, UsageChat, prompt, nil)
	if err != nil {
		return "", err
	}
	if text = chatAnswer(text); text == "" {
		return "", errors.New("the model returned an empty answer")
	}
	if repeats(text, msgs) {
		// un modelo chico atascado repite su respuesta (o su pregunta) aunque ya le contestaron: una
		// segunda vuelta con eso dicho explícitamente y, si insiste, lo armado desde la evidencia
		retry := prompt + "\n\nYour draft answer repeated an earlier answer of this conversation:\n" + clipRunes(text, 400) +
			"\nWrite a different answer that moves forward: use the evidence above and do not ask again what was already answered."
		if again, err := a.generate(ctx, UsageChat, retry, nil); err == nil {
			if again = chatAnswer(again); again != "" && !repeats(again, msgs) {
				return again, nil
			}
		}
		if extra.Fallback != "" {
			return extra.Fallback, nil
		}
	}
	return text, nil
}

// chatAnswer is the model's text without the conversation it copied first: a small model sometimes
// starts with "User: <the question>" and answers after "Trace Buddy:".
func chatAnswer(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "User:") {
		if i := strings.Index(text, "Trace Buddy:"); i >= 0 {
			text = text[i:]
		}
	}
	return strings.TrimSpace(strings.TrimPrefix(text, "Trace Buddy:"))
}

// clipRunes cuts s to n characters without splitting one (questions in Spanish carry accents).
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

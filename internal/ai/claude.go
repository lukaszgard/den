package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// DefaultModel explains diagnostics unless den.yaml's ai.model names another.
const DefaultModel = "claude-opus-5"

const systemPrompt = `You help a developer fix their local setup for den, a command-line tool that opens
tunnels (AWS SSM, SSH, EC2 Instance Connect, kubectl) to private cloud databases and caches
and keeps their IAM credentials fresh. You get the output of "den doctor": one line per check
with its status and details, and den's suggested fix where it has one.

Explain what is wrong in plain language, most important first, and give exact steps to fix
each problem — commands to run, lines to change. Group problems that share one cause. Skip
checks that passed. Stay under 250 words.

Identifiers are replaced with placeholders such as profile-1, host-2 or service-3. Use the
placeholders exactly as written; never guess the real values. Only state what the report
supports: if a cause is uncertain, say what to check.`

// Explainer turns a diagnostics report into an explanation.
type Explainer interface {
	Explain(ctx context.Context, report string) (string, error)
}

// Claude explains reports with the Anthropic API. Credentials come from the
// environment the SDK reads: ANTHROPIC_API_KEY, or a profile from `ant auth login`.
type Claude struct {
	client anthropic.Client
	model  string
}

// NewClaude returns an Explainer using model, or DefaultModel when empty.
// opts configure the API client (tests point it at a local server).
func NewClaude(model string, opts ...option.RequestOption) *Claude {
	if model == "" {
		model = DefaultModel
	}
	return &Claude{client: anthropic.NewClient(opts...), model: model}
}

// Model is the model the explainer calls.
func (c *Claude) Model() string { return c.model }

// Explain sends the (already redacted) report and returns Claude's answer.
// Server-side fallbacks are on, so a request one model declines can be
// answered by another inside the same call.
func (c *Claude) Explain(ctx context.Context, report string) (string, error) {
	msg, err := c.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: 16000,
		System:    []anthropic.BetaTextBlockParam{{Text: systemPrompt}},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("den doctor report:\n\n" + report)),
		},
		Fallbacks: anthropic.BetaFallbacksParamOfDefault(),
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
	})
	if err != nil {
		return "", explainAPIError(err)
	}
	if msg.StopReason == anthropic.BetaStopReasonRefusal {
		return "", fmt.Errorf("the model declined to answer (%s)", msg.StopDetails.Category)
	}
	var b strings.Builder
	for _, block := range msg.Content {
		if text, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			b.WriteString(text.Text)
		}
	}
	if b.Len() == 0 {
		return "", errors.New("the model returned no text")
	}
	return b.String(), nil
}

// explainAPIError says what to do about the API errors a user can fix.
func explainAPIError(err error) error {
	apiErr, ok := errors.AsType[*anthropic.Error](err)
	if !ok {
		if strings.Contains(err.Error(), "api key") || strings.Contains(strings.ToLower(err.Error()), "credential") {
			return fmt.Errorf("no Anthropic credentials: set ANTHROPIC_API_KEY or run `ant auth login` (%w)", err)
		}
		return fmt.Errorf("could not reach the Anthropic API: %w", err)
	}
	switch apiErr.StatusCode {
	case 401, 403:
		return errors.New("the Anthropic API rejected the credentials: check ANTHROPIC_API_KEY or `ant auth status`")
	case 404:
		return errors.New("the model is not available to this account: set ai.model in den.yaml to one it can use")
	case 429:
		return errors.New("rate limited by the Anthropic API: try again in a minute")
	}
	if apiErr.StatusCode >= 500 {
		return fmt.Errorf("the Anthropic API had a problem (HTTP %d): try again shortly", apiErr.StatusCode)
	}
	return fmt.Errorf("the Anthropic API refused the request (HTTP %d): %w", apiErr.StatusCode, err)
}

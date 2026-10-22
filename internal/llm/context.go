package llm

// context.go — an HTTP reply whose body says the request does not fit the
// endpoint's context window.
//
// The bare wording matters enough to type apart: "request (5701 tokens)
// exceeds the available context size (4096 tokens)" is not a payload the user
// can shave — it is a property of the model they are sitting on, and for a
// llama.cpp launch the fix is restarting the server with a larger -c. The UI
// can only offer that restart when the failure is recognisable as this one,
// which the plain providerError is not.

import (
	"regexp"
	"strconv"
	"strings"
)

// ContextExceededError is a reply whose body says the request is larger than
// the endpoint's context window. It is a providerError underneath, so the
// failover pool classifies it exactly as before: a 400 is about the request,
// not the host, and walking the pool would burn every member on the same
// verdict.
//
// Numbers carries the token counts the body named, best effort: llama.cpp puts
// the request's size first and the window second, OpenAI and vLLM put the
// window first — so the order is not interpretable and neither is read as
// either role. What survives the ambiguity is the larger of the two, which is
// the input to whichever restart figure is computed from it.
type ContextExceededError struct {
	providerError
	Numbers []int
}

// Unwrap hands the chain to the providerError underneath, not to the error it
// wraps — the promoted method of the embedded field would skip a level, and
// with it the failover pool's classification of a 400: the pool must still
// refuse to route a rejected payload to the next endpoint.
func (e *ContextExceededError) Unwrap() error { return &e.providerError }

// itIsLocalModelDefault is the figure SuggestedContext falls back to when the
// body named no numbers at all. It mirrors discover.DefaultContext without
// importing it — llm must not depend on the packages above it.
const contextSizeFallback = 16384

// SuggestedContext returns the context size worth restarting llama-server
// with: twice the larger number the body named, rounded up to a multiple of
// 1024. Halving nothing and rounding up covers the summary and the tail the
// next turn brings below it; a bare doubling of a 4096 window leaves the same
// turn that just failed sitting right on the new ceiling.
func (e *ContextExceededError) SuggestedContext() int {
	best := 0
	for _, n := range e.Numbers {
		if n > best {
			best = n
		}
	}
	if best <= 0 {
		return contextSizeFallback
	}
	// "tokens" in a context message arrives in whole thousands in practice,
	// so the rounding is to a line the user can recognise rather than a bit of
	// grime above 2*best.
	n := 2 * best
	return ((n + 1023) / 1024) * 1024
}

// contextExceededPhrases are the fixed strings the OpenAI-compatible servers
// print when the request does not fit the window. Matched against a lowercase
// copy of the body, after the JSON `error.message` has been pulled out — the
// same way rejectsStreamOptions reads a body, because a provider-specific
// complaint is only recognisable by what it says.
var contextExceededPhrases = []string{
	"exceeds the available context size", // llama.cpp / llama-server
	"exceeds context length",             // Ollama: "input length exceeds context length"
	"maximum context length",             // OpenAI, vLLM: "maximum context length is N tokens"
}

// contextTokensRe pulls every "N tokens" out of the body. The two shapes worth
// reading both use that spelling: "request (5701 tokens)" and "is 8192
// tokens. However, you requested…".
var contextTokensRe = regexp.MustCompile(`(\d+)\s*tokens`)

// contextExceeded reports whether an HTTP reply body describes a prompt too
// large for the serving context window.
func contextExceeded(body string) bool {
	msg := strings.ToLower(body)
	for _, phrase := range contextExceededPhrases {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return false
}

// contextTokenCounts extracts the token counts the body names, best effort.
// A body with no numbers at all is a body the restart figure cannot be
// computed from — the caller answers with its own default.
func contextTokenCounts(body string) []int {
	matches := contextTokensRe.FindAllStringSubmatch(body, 8)
	out := make([]int, 0, len(matches))
	for _, m := range matches {
		n, err := strconv.Atoi(m[1])
		if err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

// describeAsContextExceeded wraps base with the typed error when the body
// reads as a full context window. The message stays byte-identical — the body
// is the diagnosis — and only the type is new.
func describeAsContextExceeded(base *providerError, body string) error {
	if !contextExceeded(body) {
		return base
	}
	return &ContextExceededError{providerError: *base, Numbers: contextTokenCounts(body)}
}

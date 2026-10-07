package llm

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// A tool call whose arguments the output limit cut short is worth one more
// attempt, and a malformed one is not. That difference is the whole reason this
// is a type rather than a string: "unexpected end of JSON input" says what
// happened to the parser, not that the model was still writing when it stopped,
// and the two need opposite reactions — one is asked again, the other is
// reported.
type truncatedCallError struct {
	// tool is the call that was cut off, named in the message so the user can
	// tell a long edit from a long grep.
	tool string
	// spent is what the server reported the model was allowed to produce before
	// it stopped, 0 when the endpoint reported no usage. It is the size of the
	// wall the call ran into, which is the one number that tells the user which
	// knob to turn.
	spent int
	// err is the parse failure itself, kept for the unwrap.
	err error
}

func (e *truncatedCallError) Error() string {
	spent := ""
	if e.spent > 0 {
		spent = fmt.Sprintf(", упёрся в %d токенов", e.spent)
	}
	return fmt.Sprintf("dmcode: вызов %s дошёл не целиком — модель упёрлась в лимит вывода (finish_reason=length%s): %v. Попросите модель писать файл по частям — короткий write_file, остальное добавлением через edit_file — или поднимите лимит вывода модели.", e.tool, spent, e.err)
}

func (e *truncatedCallError) Unwrap() error { return e.err }

// truncationNote is what the second attempt is told. It has to be true as well
// as useful: the call really was discarded, so "do it again" is not a request
// to repeat work that already landed, and saying so is what keeps the model from
// re-reading a file to check whether the edit applied. The write_file tail is
// the recipe the failure actually calls for: a file too big for one call is
// written as a head plus anchored appends, which no model guesses on its own
// after being told "smaller pieces" once.
const truncationNote = "Your previous call to %s was cut off by the output limit before its arguments were complete. Nothing was applied: the call was discarded, not half-applied. Make the call again now, in smaller pieces — several small calls rather than one large one — and keep any text before the call down to a single sentence. If it was write_file: write only the head of the file first (a few dozen lines), then append the rest with edit_file, anchoring each edit on the last line the previous chunk wrote."

// Bounds on the second attempt's output budget. The floor keeps a server that
// stopped at a few dozen tokens from being asked for a few dozen more; the
// ceiling keeps a generous endpoint from being asked for an absurd number by a
// call that was never going to fit in any of them.
const (
	minReAskTokens = 4096
	maxReAskTokens = 32768
)

// chatCall is one request against one endpoint, with the body it is about to
// send — the re-ask needs to change the body, not just ask again.
type chatCall func(context.Context, chatRequest) iter.Seq2[*model.LLMResponse, error]

// withReAsk runs a call and, when the only thing wrong with it was that the
// model ran out of room halfway through a tool call, asks the same endpoint the
// same question once more with a note asking for a smaller call.
//
// Two rules keep the second attempt honest:
//
//   - It only happens once. A model that will not split a call after being
//     told to is not going to on the third try either, and each try costs a
//     full generation.
//   - Text the first attempt already streamed stays on screen — the
//     transcript cannot take it back — but a lost turn is worse than a
//     duplicated sentence. The second attempt is preceded by a blank line,
//     so the narration it redoes lands on its own row instead of glued to
//     the half-answer the first attempt left behind.
//
// Anything that goes wrong on the second attempt — a transport failure, a
// rejected budget, a second truncation — is reported as the truncation it grew
// out of, because that is the cause the user can act on.
func withReAsk(ctx context.Context, body chatRequest, call chatCall) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		var trunc *truncatedCallError
		emitted := false
		for resp, err := range call(ctx, body) {
			if err != nil {
				if !errors.As(err, &trunc) {
					yield(nil, err)
					return
				}
				break
			}
			if resp == nil {
				continue
			}
			emitted = true
			if !yield(resp, nil) {
				return
			}
		}
		if trunc == nil {
			return
		}
		if ctx.Err() != nil || !reAskable(body) {
			yield(nil, trunc)
			return
		}
		if emitted {
			// The blank line between the first attempt's text and the redo.
			sep := &model.LLMResponse{
				Partial: true,
				Content: genai.NewContentFromText("\n\n", genai.RoleModel),
			}
			if !yield(sep, nil) {
				return
			}
		}
		// A second attempt that runs to completion has answered the question, and
		// the truncation it grew out of is no longer anything the user needs to
		// read. Only a failed one is reported.
		failed := false
		for resp, err := range call(ctx, reAskBody(body, trunc)) {
			if err != nil {
				failed = true
				break
			}
			if resp == nil {
				continue
			}
			if !yield(resp, nil) {
				return
			}
		}
		if failed {
			yield(nil, trunc)
		}
	}
}

// reAskable reports whether the budget on the request is dmcode's to change. A
// request that carries one was given it deliberately — the tool probe asks for
// 64 tokens on purpose, and answering a probe that stopped at its budget with a
// bigger budget would turn a five-second check into a second one.
func reAskable(body chatRequest) bool { return body.MaxOutputTokens == 0 }

// reAskBody is the request for the second attempt: the same conversation, the
// same tools, one note at the end, and a budget with room in it.
func reAskBody(body chatRequest, trunc *truncatedCallError) chatRequest {
	out := body
	out.MaxOutputTokens = reAskBudget(body.MaxOutputTokens, trunc.spent)
	msgs := make([]chatMessage, 0, len(body.Messages)+1)
	msgs = append(msgs, body.Messages...)
	out.Messages = append(msgs, chatMessage{
		Role:    "user",
		Content: fmt.Sprintf(truncationNote, trunc.tool),
	})
	return out
}

// reAskBudget is the output budget for the second attempt.
//
// A budget dmcode did not ask for is the server's, and the failed turn is the
// only evidence there is of how much room the call actually needs: where the
// endpoint reported its usage, twice what the call was already using is the
// smallest number that is not the same wall again. Where it reported nothing
// there is no evidence, so the budget is left exactly as it was and the note
// does the work.
func reAskBudget(current, spent int) int {
	if current > 0 || spent <= 0 {
		return current
	}
	return min(max(2*spent, minReAskTokens), maxReAskTokens)
}

// completionTokens is what the server said the model was allowed to produce, or
// 0 when it reported no usage at all — which is most streaming endpoints unless
// the request asks for usage in the final chunk.
func completionTokens(u *chatUsage) int {
	if u == nil {
		return 0
	}
	return u.CompletionTokens
}

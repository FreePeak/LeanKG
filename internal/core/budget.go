package core

import (
	"context"
	"encoding/json"
	"math"
	"strconv"

	"github.com/FreePeak/LeanKG/internal/budget"
	"github.com/FreePeak/LeanKG/internal/errs"
)

// Answer is one query answer as every transport sees it. Body is the answer
// after the token budget and is what each transport writes; Raw is the engine
// answer before the budget (capture reads it); Stats is the budget accounting.
type Answer struct {
	Body  any
	Raw   map[string]any
	Stats budget.Stats
}

// QueryAnswer runs Query and applies the token budget. It is the one place
// every transport gets its budget: REST, ConnectRPC, the web API and MCP all
// call it, so the same request is cut the same way on each.
//
// The cap is args.token_budget when the caller sets one, else the budget
// table's cap for the action (see internal/budget).
func (e *Engine) QueryAnswer(ctx context.Context, req QueryRequest) (Answer, error) {
	override, err := tokenBudgetArg(req.Args)
	if err != nil {
		return Answer{}, err
	}
	out, err := e.Query(ctx, req)
	if err != nil {
		return Answer{Raw: out}, err
	}
	body, st := EnforceBudget(out, req.Action, override)
	return Answer{Body: body, Raw: out, Stats: st}, nil
}

// EnforceBudget caps an answer at override tokens, or at the table's cap for
// the action when override is 0. An empty action is capped as the ladder
// router, "search", which answers search hits. Typed engine payloads are
// round-tripped through JSON so the budget marker can attach to them.
func EnforceBudget(v any, action string, override int) (any, budget.Stats) {
	key := action
	if key == "" {
		key = "search"
	}
	maxTokens := override
	if maxTokens == 0 {
		maxTokens = budget.TokenBudget{}.MaxTokensForTool(key)
	}
	if m, ok := v.(map[string]any); ok {
		return budget.TokenBudget{}.ApplyMax(m, key, maxTokens)
	}
	if raw, err := json.Marshal(v); err == nil {
		var round map[string]any
		if json.Unmarshal(raw, &round) == nil {
			return budget.TokenBudget{}.ApplyMax(round, key, maxTokens)
		}
	}
	return budget.TokenBudget{}.ApplyMax(v, key, maxTokens)
}

// tokenBudgetArg reads args.token_budget, the caller's own cap in tokens. An
// absent argument is 0 (the table's cap). A JSON number, a Go integer or a
// decimal string (the proto wire carries strings) must be a positive whole
// number.
func tokenBudgetArg(args map[string]any) (int, error) {
	return ParseTokenBudget(args["token_budget"])
}

// ResolveTokenBudget is the token cap one answer of action gets, by the rule
// QueryAnswer applies: the caller's token_budget when set, else the action's
// table cap. A transport with its own request field calls it so its answer is
// cut by the same rule as every other transport.
func ResolveTokenBudget(action string, token any) (int, error) {
	override, err := ParseTokenBudget(token)
	if err != nil {
		return 0, err
	}
	if override > 0 {
		return override, nil
	}
	return budget.TokenBudget{}.MaxTokensForTool(action), nil
}

// ParseTokenBudget reads a token budget from a decoded request value: a JSON
// number, a Go integer or a decimal string. nil is absent (0). Anything else
// must be a positive whole number.
func ParseTokenBudget(v any) (int, error) {
	if v == nil {
		return 0, nil
	}
	var n int64
	switch t := v.(type) {
	case float64:
		if t != math.Trunc(t) || t > math.MaxInt32 || t < 1 {
			return 0, invalidTokenBudget()
		}
		return int(t), nil
	case int:
		n = int64(t)
	case int32:
		n = int64(t)
	case int64:
		n = t
	case string:
		p, err := strconv.ParseInt(t, 10, 64)
		if err != nil {
			return 0, invalidTokenBudget()
		}
		n = p
	default:
		return 0, invalidTokenBudget()
	}
	if n < 1 || n > math.MaxInt32 {
		return 0, invalidTokenBudget()
	}
	return int(n), nil
}

func invalidTokenBudget() error {
	return errs.NewError(errs.InvalidParam, "token_budget must be a positive whole number", "")
}

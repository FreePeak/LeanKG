package metrics

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/FreePeak/LeanKG/internal/telemetry/report"
)

// fallbackK is the number of follow-up tool calls after a LeanKG call in
// which a discovery call outside the hits counts as a fallback (DS-17).
const fallbackK = 5

// Transcript is GET /sessions/{id}/transcript. With a transcript source and a
// linked session it replays the agent's own turns, enriched with LeanKG calls.
// Otherwise it shows a synthetic timeline of the captured calls and says why.
// ok is false when the session is unknown.
func Transcript(ctx context.Context, st telemetry.Store, id string, src TranscriptSource) (report.Transcript, bool, error) {
	s, ok, err := st.Session(ctx, id)
	if err != nil || !ok {
		return report.Transcript{Messages: []report.Message{}}, false, err
	}
	calls, err := toolCalls(ctx, st, telemetry.CallFilter{SessionID: id})
	if err != nil {
		return report.Transcript{Messages: []report.Message{}}, false, err
	}
	sortCalls(calls)
	links, err := st.Links(ctx, id)
	if err != nil {
		return report.Transcript{Messages: []report.Message{}}, false, err
	}
	linked := s.LinkStatus == "linked" || len(links) > 0
	switch {
	case src == nil:
		return syntheticTranscript(id, calls, "transcripts are not granted for client "+s.ClientName+"; showing LeanKG calls only"), true, nil
	case !linked:
		return syntheticTranscript(id, calls, "session is not linked to its transcript; showing LeanKG calls only"), true, nil
	}
	turns, err := src(ctx, s)
	if err != nil {
		return syntheticTranscript(id, calls, "transcript unavailable ("+err.Error()+"); showing LeanKG calls only"), true, nil
	}
	if len(turns) == 0 {
		return syntheticTranscript(id, calls, "transcript has no readable turns (moved, or not granted for "+s.ClientName+"); showing LeanKG calls only"), true, nil
	}
	return linkedTranscript(id, turns, calls, links), true, nil
}

func syntheticTranscript(id string, calls []telemetry.CallEvent, reason string) report.Transcript {
	msgs := []report.Message{}
	for _, c := range calls {
		row := callRow(c)
		msgs = append(msgs, report.Message{
			ID:        "call:" + c.ID,
			Role:      "assistant",
			CreatedAt: c.TS,
			Tokens:    c.OutTokensPost,
			Content: []report.MessagePart{{
				Type: "tool-call",
				Tool: &report.ToolCallPart{
					Type:       "tool-call",
					ToolCallID: c.ID,
					ToolName:   "leankg." + c.Tool,
					ArgsText:   syntheticArgs(c),
					Result:     syntheticResult(c),
					IsError:    isError(c.Outcome),
					LeanKG:     &row,
				},
			}},
		})
	}
	return report.Transcript{SessionID: id, Synthetic: true, Reason: reason, Messages: msgs}
}

func syntheticArgs(c telemetry.CallEvent) string {
	if c.ArgsRedacted != "" {
		return c.ArgsRedacted
	}
	b, _ := json.Marshal(map[string]string{"arg_keys": c.ArgKeys})
	return string(b)
}

func syntheticResult(c telemetry.CallEvent) string {
	if c.BodyRedacted != "" {
		return c.BodyRedacted
	}
	if c.OutcomeReason != "" {
		return c.Outcome + ": " + c.OutcomeReason
	}
	return c.Outcome
}

// linkedTranscript maps the agent's turns to messages. A tool call is
// enriched with the captured LeanKG call it corresponds to (by the link's
// tool_use_id, else by args hash and nearest time). A discovery call that
// follows a LeanKG call within fallbackK calls and touches a file outside
// that call's hits is marked as a fallback.
func linkedTranscript(id string, turns []sessionlink.Turn, calls []telemetry.CallEvent, links []telemetry.SessionLink) report.Transcript {
	callByID := make(map[string]telemetry.CallEvent, len(calls))
	for _, c := range calls {
		callByID[c.ID] = c
	}
	linkBy := linksByCall(links)
	callByToolUse := map[string]string{}
	for _, l := range links {
		if l.ToolUseID != "" {
			callByToolUse[l.ToolUseID] = l.CallID
		}
	}
	used := map[string]bool{}
	parts := map[string]*report.ToolCallPart{}
	var win struct {
		active    bool
		hits      []string
		remaining int
	}

	attach := func(tc sessionlink.ToolCall) *telemetry.CallEvent {
		if cid := callByToolUse[tc.ID]; cid != "" {
			if c, ok := callByID[cid]; ok {
				used[cid] = true
				return &c
			}
		}
		if !tc.IsLeanKG {
			return nil
		}
		var best *telemetry.CallEvent
		var bestGap time.Duration
		for i := range calls {
			c := calls[i]
			if used[c.ID] || c.ArgsHash == "" || c.ArgsHash != tc.ArgsHash {
				continue
			}
			gap := c.TS.Sub(tc.TS)
			if gap < 0 {
				gap = -gap
			}
			if best == nil || gap < bestGap {
				best, bestGap = &calls[i], gap
			}
		}
		if best != nil {
			used[best.ID] = true
			return best
		}
		return nil
	}

	toolPart := func(tc sessionlink.ToolCall) *report.ToolCallPart {
		p := &report.ToolCallPart{
			Type:       "tool-call",
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			ArgsText:   tc.Args,
			Result:     tc.Result,
			IsError:    tc.IsError,
		}
		if c := attach(tc); c != nil {
			row := rowWithSignals(*c, linkBy) // same signals as the session detail
			p.LeanKG = &row
			win.active = true
			win.hits = splitFiles(c.HitFiles)
			win.remaining = fallbackK
		} else if tc.IsLeanKG {
			win.active = false
		} else if win.active && win.remaining > 0 {
			win.remaining--
			if sessionlink.IsDiscovery(tc.Norm) && !matchesAny(tc.Target, win.hits) {
				p.Fallback = true
			}
		}
		parts[tc.ID] = p
		return p
	}

	msgs := []report.Message{}
	for i, t := range turns {
		mid := func(kind string) string { return kind + ":" + id + ":" + strconv.Itoa(i) }
		switch t.Role {
		case "user":
			win.active = false
			if t.Text == "" {
				continue
			}
			msgs = append(msgs, report.Message{
				ID: mid("user"), Role: "user", CreatedAt: t.TS,
				Content: []report.MessagePart{{Type: "text", Text: t.Text}},
			})
		case "assistant":
			m := report.Message{ID: mid("assistant"), Role: "assistant", CreatedAt: t.TS, Tokens: t.OutputTokens, Content: []report.MessagePart{}}
			if t.Text != "" {
				m.Content = append(m.Content, report.MessagePart{Type: "text", Text: t.Text})
			}
			for _, tc := range t.ToolCalls {
				m.Content = append(m.Content, report.MessagePart{Type: "tool-call", Tool: toolPart(tc)})
			}
			if len(m.Content) > 0 {
				msgs = append(msgs, m)
			}
		case "tool":
			for _, tc := range t.ToolCalls {
				if p, ok := parts[tc.ID]; ok {
					p.Result = tc.Result
					p.IsError = tc.IsError
					continue
				}
				msgs = append(msgs, report.Message{
					ID: mid("tool"), Role: "assistant", CreatedAt: t.TS,
					Content: []report.MessagePart{{Type: "tool-call", Tool: toolPart(tc)}},
				})
			}
		}
	}
	return report.Transcript{SessionID: id, Synthetic: false, Messages: msgs}
}

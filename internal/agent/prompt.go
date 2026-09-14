package agent

import (
	"fmt"
	"strings"
)

// contextBudget caps how much of cp.md is put in front of the model.
//
// The planner here is a local model on the node's own GPU with a finite window
// that is also serving customers. cp.md grows with every model ever run, and an
// agent whose context is mostly history has less room to reason about the
// question actually asked.
const contextBudget = 12000

// systemPrompt tells the model what it is operating and how to reply.
//
// It states the refusals rather than leaving them to be discovered. The
// guardrails will refuse regardless, but a model that knows a rule spends its
// reasoning on what it can do instead of proposing something that cannot
// happen and then being told so.
func (a *Agent) systemPrompt() string {
	var b strings.Builder

	b.WriteString(`You operate a GPU node on the Swan Inference marketplace. You are given a goal
and a set of tools. Work towards the goal one tool at a time.

Reply with a single JSON object each turn:

  thought  what you are doing and why, in one or two sentences
  tool     the tool to run, or "" when you are finished
  args     that tool's arguments as an object
  done     true when you have an answer for the operator
  answer   your reply to the operator, when done is true

Rules:
- Use one tool per turn, then read its result before deciding the next.
- Never invent a tool name or an argument. If a tool fails, read the error and
  correct it rather than repeating the same call.
- Prefer what this node has measured over anything estimated. A model this node
  has already run is a fact; an estimate is not.
- When you have enough to answer, set done and answer. Do not keep calling
  tools to confirm something you already know.
- Be specific in your answer: name models, figures and units. The operator is
  reading it to make a decision.
- EVERY figure you state must come from a tool result in this conversation.
  Never answer from memory, from what is typical, or from what a node like this
  usually earns. If you have not run a tool, you do not know the answer — run
  one. Inventing numbers an operator will act on is the worst thing you can do
  here, worse than saying you could not find out.
`)

	if !a.AllowActions {
		b.WriteString(`
This run is READ-ONLY. Tools that change the node will be refused. Investigate
and report; recommend what should change rather than trying to change it.
`)
	} else {
		b.WriteString(`
This run MAY change the node. Before starting or stopping anything, check it
against what the node has measured and what it is serving now. Say what you are
about to do, and why, in your thought.
`)
	}

	b.WriteString("\n## Tools\n\n")
	b.WriteString(a.Tools.Describe(a.AllowActions))

	if ctx := strings.TrimSpace(a.Context); ctx != "" {
		b.WriteString("\n## This node\n\n")
		b.WriteString(truncateContext(ctx, contextBudget))
		b.WriteString("\n")
	}

	return b.String()
}

// truncateContext trims cp.md to fit, keeping the beginning.
//
// The beginning holds the node's hardware, its limits and the rules — the parts
// that change what an answer should be. The tail is per-model history, which is
// reachable through a tool when it is actually needed.
func truncateContext(s string, budget int) string {
	if len(s) <= budget {
		return s
	}
	cut := s[:budget]
	if i := strings.LastIndex(cut, "\n## "); i > budget/2 {
		cut = cut[:i]
	}
	return cut + fmt.Sprintf("\n\n_(truncated; %d further characters are available through the tools)_\n", len(s)-len(cut))
}

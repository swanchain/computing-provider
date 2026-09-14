// Package agent runs a local model as an operator for this node.
//
// The model is given a goal in plain language, the node's own cp.md as context,
// and a fixed set of tools. It cannot run shell commands and it cannot call
// anything not in that set, so the worst a confused or manipulated model can do
// is bounded by what the tools themselves allow — which is why the tools are
// small, named, and individually reviewable rather than a general escape hatch.
//
// Tools that change the node are refused unless the operator explicitly allowed
// them, and every one of those still goes through the same guardrails a human
// running the same command would hit. The agent is a way to *reach* the
// existing safety machinery conversationally, never a way around it.
package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Arg describes one tool parameter.
type Arg struct {
	Name        string
	Description string

	// Type is "string", "integer" or "boolean". Constrained decoding holds
	// the model to it, so a tool never has to defend against a string where
	// it expected a number.
	Type string

	Required bool
}

// Tool is one thing the agent can do.
type Tool struct {
	Name        string
	Description string
	Args        []Arg

	// Acts marks a tool that changes the node. These are refused unless the
	// operator passed --allow-actions, and the distinction is a property of
	// the tool rather than a judgement the model makes about its own
	// intentions.
	Acts bool

	// Run executes the tool and returns what the model should see.
	//
	// Errors are returned to the model as text rather than ending the run: a
	// tool that failed because an argument was wrong is something the model
	// can correct, and aborting would waste the whole episode on a typo.
	Run func(ctx context.Context, args Args) (string, error)
}

// Args are the arguments a model supplied, already type-checked.
type Args map[string]interface{}

// String returns a string argument, or "" when absent.
func (a Args) String(name string) string {
	if v, ok := a[name].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// Int returns an integer argument, or 0 when absent.
//
// Accepts a float because JSON numbers decode that way, and a numeric string
// because a model that has been told "integer" still occasionally quotes it.
func (a Args) Int(name string) int {
	switch v := a[name].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err == nil {
			return n
		}
	}
	return 0
}

// Bool returns a boolean argument.
func (a Args) Bool(name string) bool {
	v, _ := a[name].(bool)
	return v
}

// Registry holds the tools available in one run.
type Registry struct {
	tools map[string]*Tool
}

// NewRegistry builds a registry from a list of tools.
func NewRegistry(tools ...*Tool) *Registry {
	r := &Registry{tools: map[string]*Tool{}}
	for _, t := range tools {
		r.tools[t.Name] = t
	}
	return r
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (*Tool, bool) {
	t, ok := r.tools[strings.TrimSpace(name)]
	return t, ok
}

// Names lists every tool, in a stable order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// All returns every tool, ordered by name.
func (r *Registry) All() []*Tool {
	out := make([]*Tool, 0, len(r.tools))
	for _, name := range r.Names() {
		out = append(out, r.tools[name])
	}
	return out
}

// Describe renders the tool list for the model's prompt.
//
// Written as a list rather than a schema because the schema already constrains
// what the model may emit; this exists to say what each tool is *for*, which a
// schema cannot.
func (r *Registry) Describe(allowActions bool) string {
	var b strings.Builder
	for _, t := range r.All() {
		b.WriteString("- `" + t.Name + "`")
		if t.Acts {
			if allowActions {
				b.WriteString(" **(changes the node)**")
			} else {
				b.WriteString(" **(changes the node — NOT permitted in this run)**")
			}
		}
		b.WriteString(": " + t.Description + "\n")
		for _, a := range t.Args {
			required := ""
			if a.Required {
				required = ", required"
			}
			b.WriteString(fmt.Sprintf("    - `%s` (%s%s): %s\n", a.Name, a.Type, required, a.Description))
		}
	}
	return b.String()
}

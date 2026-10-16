package core

import (
	"context"
	"strings"
	"time"
)

// External confirmation (docs/ai.md §5): an AI client asks the user, in the
// launcher window, before an action that changes something. Requests are
// shown one at a time over whatever view is open; the others wait in a
// queue. A request without an answer within ExternalTimeout is denied.

// ExternalTimeout is how long an external confirmation waits for the user.
var ExternalTimeout = 60 * time.Second

// ExternalRequest is a confirmation asked for by an AI client.
type ExternalRequest struct {
	// Client names who asks, e.g. "Claude Desktop" or "Claude (AI Chat)".
	Client string
	// Action is the action's (or the command's) title; Item the item it acts
	// on ("" for a command); Plugin the plugin's name.
	Action string
	Item   string
	Plugin string
	// Destructive asks with the danger style and never offers AllowSession.
	Destructive bool
	// EndsSession warns that the Windows session ends (and with it the
	// background tasks), as the launcher's own confirmation does.
	EndsSession bool
	// AllowSession offers "Allow for this session".
	AllowSession bool
	// InPlace marks a request from the launcher's own AI chat: the front end
	// shows the window without going back to the root search, so the dialog
	// appears over the chat.
	InPlace bool
}

// Decision is the user's answer to an ExternalRequest.
type Decision int

const (
	// DecisionDeny: the user declined, or did not answer in time.
	DecisionDeny Decision = iota
	// DecisionAllow: run it this once.
	DecisionAllow
	// DecisionAllowSession: run it, and the same action again without
	// asking for the rest of the client's session.
	DecisionAllowSession
)

func (d Decision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionAllowSession:
		return "allow-session"
	}
	return "deny"
}

// extPending is a queued external request.
type extPending struct {
	req  ExternalRequest
	done chan Decision // buffered; receives the decision once
}

// OnExternalConfirm sets fn to be called (on a background goroutine) when an
// external confirmation comes up for display: the front end shows the
// launcher window if it is hidden.
func (c *Controller) OnExternalConfirm(fn func(ExternalRequest)) {
	c.mu.Lock()
	c.onExternal = fn
	c.mu.Unlock()
}

// ConfirmExternal asks the user to confirm req and waits for the answer, at
// most ExternalTimeout or until ctx ends (both deny). It is safe from any
// goroutine; requests are answered one at a time in order.
func (c *Controller) ConfirmExternal(ctx context.Context, req ExternalRequest) Decision {
	if req.Destructive {
		req.AllowSession = false
	}
	p := &extPending{req: req, done: make(chan Decision, 1)}
	c.mu.Lock()
	c.external = append(c.external, p)
	head := len(c.external) == 1
	c.mu.Unlock()
	if head {
		c.externalShown(p)
	}
	timer := time.NewTimer(ExternalTimeout)
	defer timer.Stop()
	select {
	case d := <-p.done:
		return d
	case <-timer.C:
	case <-ctx.Done():
	}
	c.resolveExternal(p, DecisionDeny)
	return <-p.done
}

// externalShown tells the front end that p is now displayed.
func (c *Controller) externalShown(p *extPending) {
	c.mu.Lock()
	show, notify := c.onExternal, c.onChange
	c.mu.Unlock()
	if show != nil {
		show(p.req)
	}
	if notify != nil {
		notify()
	}
}

// resolveExternal answers p (if not answered yet) and shows the next
// request.
func (c *Controller) resolveExternal(p *extPending, d Decision) {
	c.mu.Lock()
	i := -1
	for j, q := range c.external {
		if q == p {
			i = j
			break
		}
	}
	if i < 0 {
		c.mu.Unlock()
		return
	}
	c.external = append(c.external[:i:i], c.external[i+1:]...)
	var next *extPending
	if i == 0 && len(c.external) > 0 {
		next = c.external[0]
	}
	notify := c.onChange
	c.mu.Unlock()
	p.done <- d
	if next != nil {
		c.externalShown(next)
	} else if notify != nil {
		notify()
	}
}

// externalKey answers the displayed external request with a key: Enter
// allows, Ctrl+Enter allows for the session (when offered), Escape denies.
// It reports whether a request is displayed. c.mu is held on entry and
// released before returning.
func (c *Controller) externalKey(key string) bool {
	if len(c.external) == 0 {
		c.mu.Unlock()
		return false
	}
	p := c.external[0]
	c.mu.Unlock()
	switch key {
	case "Enter":
		c.resolveExternal(p, DecisionAllow)
	case "Ctrl+Enter":
		if p.req.AllowSession {
			c.resolveExternal(p, DecisionAllowSession)
		}
	case "Escape":
		c.resolveExternal(p, DecisionDeny)
	}
	return true
}

// externalSnapshot is the dialog of the displayed external request. c.mu
// must be held.
func (c *Controller) externalSnapshot() *ConfirmSnapshot {
	if len(c.external) == 0 {
		return nil
	}
	r := c.external[0].req
	core := c.eng.core
	what := r.Action
	if r.Item != "" && r.Item != r.Action {
		what = core.T("confirm.external.what", "action", r.Action, "item", r.Item)
	}
	if r.Plugin != "" {
		what = core.T("confirm.external.inPlugin", "what", what, "plugin", r.Plugin)
	}
	lines := []string{what}
	if r.EndsSession {
		msg := core.T("confirm.messageSession")
		if ts := c.eng.tasks; ts != nil {
			if active, _ := ts.Indicator(); active > 0 {
				msg = core.N("confirm.tasksInterrupted", active)
			}
		}
		lines = append(lines, msg)
	} else if r.Destructive {
		lines = append(lines, core.T("confirm.message"))
	}
	lines = append(lines, core.N("confirm.external.timeout", int(ExternalTimeout/time.Second)))
	s := &ConfirmSnapshot{
		Title:    core.T("confirm.external.title", "client", r.Client),
		Message:  strings.Join(lines, "\n"),
		Confirm:  core.T("confirm.external.allow"),
		Cancel:   core.T("confirm.external.deny"),
		External: true,
		Danger:   r.Destructive,
		Queued:   len(c.external) - 1,
	}
	if r.AllowSession {
		s.AllowSession = core.T("confirm.external.allowSession")
	}
	return s
}

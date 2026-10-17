package agentrt

import (
	"log/slog"
	"sync"
	"time"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// PermissionTimeout is how long a permission request of an agent waits for
// the user's answer before it is rejected (the launcher's confirmation dialog
// denies after the same time, docs/ai.md §5).
var PermissionTimeout = 60 * time.Second

// permissions are the permission requests of one turn (TurnIO.Emit): each
// is answered once, by the user, by the timeout, by a stop or by the end of
// the turn, all of which but the user reject.
type permissions struct {
	log *slog.Logger

	mu      sync.Mutex
	pending map[*pendingPermission]bool
	closed  bool
}

// pendingPermission is a request not answered yet.
type pendingPermission struct {
	once    sync.Once
	respond func(string)
	done    chan struct{}
	timer   *time.Timer
}

func newPermissions(log *slog.Logger) *permissions {
	return &permissions{log: log, pending: map[*pendingPermission]bool{}}
}

// track returns a copy of req whose Respond answers once and whose Done is
// closed then; it is rejected after PermissionTimeout.
func (ps *permissions) track(req *backend.PermissionRequest) *backend.PermissionRequest {
	out := *req
	p := &pendingPermission{respond: req.Respond, done: make(chan struct{})}
	out.Done = p.done
	out.Respond = func(id string) { ps.answer(p, id, "user") }
	ps.mu.Lock()
	closed := ps.closed
	if !closed {
		ps.pending[p] = true
		p.timer = time.AfterFunc(PermissionTimeout, func() { ps.answer(p, "", "timeout") })
	}
	ps.mu.Unlock()
	if closed {
		ps.answer(p, "", "turn ended")
	}
	return &out
}

// answer answers p once.
func (ps *permissions) answer(p *pendingPermission, id, by string) {
	p.once.Do(func() {
		ps.mu.Lock()
		delete(ps.pending, p)
		if p.timer != nil {
			p.timer.Stop()
		}
		ps.mu.Unlock()
		ps.log.Info("agent permission request answered", "allowed", id != "", "by", by)
		if p.respond != nil {
			p.respond(id)
		}
		close(p.done)
	})
}

// rejectAll rejects the requests not answered yet (the user stopped the
// answer).
func (ps *permissions) rejectAll() {
	ps.mu.Lock()
	var ps2 []*pendingPermission
	for p := range ps.pending {
		ps2 = append(ps2, p)
	}
	ps.mu.Unlock()
	for _, p := range ps2 {
		ps.answer(p, "", "stop")
	}
}

// close rejects what is pending at the end of the turn; requests tracked
// later are rejected at once.
func (ps *permissions) close() {
	ps.mu.Lock()
	ps.closed = true
	ps.mu.Unlock()
	ps.rejectAll()
}

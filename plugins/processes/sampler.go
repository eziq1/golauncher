package processes

import (
	"strings"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/process"
)

// Sampling only while shown (docs/process-manager.md §2.5): a single loop
// samples the processes once per interval while a prefixed root search or
// a process view shows. The root search is refreshed by Host.Invalidate,
// open views by Host.Render. RootItems and View only read the latest
// sample (the very first one is taken in the call).

const (
	// secondDelay is the wait before the second sample, which gives the
	// first CPU percentages.
	secondDelay = 250 * time.Millisecond
	// maxMisses stops the root refresh after this many ticks in a row
	// without a pull (the root search is no longer shown).
	maxMisses = 2
	// keepAfterHide is how long the cached data is kept after the launcher
	// was hidden.
	keepAfterHide = 30 * time.Second
)

// sampleState is the latest sample and the per-process cache.
type sampleState struct {
	prev, cur *process.Snapshot
	windows   []process.Window
	ports     map[uint32][]string
	info      map[process.ID]*pinfo
	model     *model
	group     bool // model was built with grouping
	order     map[string]int
}

// sample takes a new sample. p.mu must not be held.
func (p *Plugin) sample() {
	snap, err := p.b.Snapshot()
	if err != nil {
		p.h.Logger().Warn("processes: snapshot", "err", err)
		return
	}
	wins, err := p.b.Windows()
	if err != nil {
		p.h.Logger().Debug("processes: windows", "err", err)
	}
	ports := map[uint32][]string{}
	if socks, err := p.b.Ports(); err == nil {
		seen := map[string]bool{}
		for _, s := range socks {
			k := strings.ToUpper(s.Proto) + " " + itoa(int(s.Local.Port()))
			if !seen[itoa(int(s.PID))+k] {
				seen[itoa(int(s.PID))+k] = true
				ports[s.PID] = append(ports[s.PID], k)
			}
		}
	}
	// Read what the snapshot lacks for processes not seen before, outside
	// the lock.
	p.mu.Lock()
	known := p.st.info
	p.mu.Unlock()
	info := make(map[process.ID]*pinfo, len(snap.Procs))
	for _, sp := range snap.Procs {
		if pi, ok := known[sp.ID]; ok {
			info[sp.ID] = pi
			continue
		}
		pi := &pinfo{Info: p.b.Info(sp.ID)}
		name := pi.Description
		if name == "" {
			name = sp.Name
		}
		pi.pinyin, pi.initial = pinyinOf(name)
		info[sp.ID] = pi
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.st.prev, p.st.cur = p.st.cur, snap
	p.st.windows, p.st.ports, p.st.info = wins, ports, info
	p.st.model = nil
	p.samples++
}

// current returns the model of the latest sample, sampling first when there
// is none (twice in Sync mode, so CPU percentages are known).
func (p *Plugin) current() *model {
	p.mu.Lock()
	has := p.st.cur != nil
	p.mu.Unlock()
	if !has {
		p.sample()
		if p.opts.Sync {
			p.sample()
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.st.cur == nil {
		return &model{snap: &process.Snapshot{}, cpu: -1, procs: map[process.ID]*proc{}, byRoot: map[process.ID]*row{}, byPID: map[uint32]*proc{}}
	}
	group := p.grouping()
	if p.st.model == nil || p.st.group != group {
		p.st.model, p.st.group = p.buildModel(&p.st, group), group
	}
	return p.st.model
}

// rootPulled records a prefixed RootItems call and starts the loop.
func (p *Plugin) rootPulled() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.opts.Sync {
		return
	}
	p.rootActive, p.pulled = true, true
	p.startLocked()
}

// stopRoot ends the root's use of the loop.
func (p *Plugin) stopRoot() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rootActive = false
}

// viewPulled records a pull of an open view and starts the loop.
func (p *Plugin) viewPulled(instance string, v viewState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || instance == "" || p.opts.Sync {
		return
	}
	p.views[instance] = v
	p.startLocked()
}

// ViewClosed implements plugin.ViewCloser.
func (p *Plugin) ViewClosed(instance string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.views, instance)
}

// startLocked starts the loop if it is not running. p.mu must be held.
func (p *Plugin) startLocked() {
	if p.stop != nil {
		return
	}
	p.cancelIdleLocked()
	stop := make(chan struct{})
	p.stop = stop
	p.wg.Add(1)
	p.running.Add(1)
	p.h.Go(func() {
		defer p.wg.Done()
		defer p.running.Add(-1)
		p.loop(stop)
	})
}

// stopLocked stops the loop. p.mu must be held.
func (p *Plugin) stopLocked() {
	if p.stop != nil {
		close(p.stop)
		p.stop = nil
	}
}

func (p *Plugin) loop(stop chan struct{}) {
	for {
		p.mu.Lock()
		wait := p.interval()
		if p.samples < 2 {
			wait = secondDelay
		}
		p.mu.Unlock()
		select {
		case <-stop:
			return
		case <-p.opts.After(wait):
		}
		select {
		case <-stop:
			return
		default:
		}
		p.sample()
		if !p.tick(stop) {
			return
		}
	}
}

// tick pushes the new sample to its users; it returns false (and ends the
// loop) when nothing uses it any more.
func (p *Plugin) tick(stop chan struct{}) bool {
	p.mu.Lock()
	if p.stop != stop {
		p.mu.Unlock()
		return false
	}
	root := false
	if p.rootActive {
		if p.pulled {
			p.misses = 0
		} else {
			p.misses++
		}
		p.pulled = false
		if p.misses >= maxMisses {
			p.rootActive, p.misses = false, 0
		} else {
			root = true
		}
	}
	views := make(map[string]viewState, len(p.views))
	for k, v := range p.views {
		views[k] = v
	}
	if !root && len(views) == 0 {
		p.stopLocked()
		p.mu.Unlock()
		return false
	}
	p.mu.Unlock()
	if root {
		p.h.Invalidate("")
	}
	for inst, v := range views {
		view, err := p.render(v)
		if err != nil {
			continue
		}
		p.mu.Lock()
		_, open := p.views[inst]
		p.mu.Unlock()
		if open {
			p.h.Render(inst, view)
		}
	}
	return true
}

// windowHidden stops sampling and arms the release of the cache.
func (p *Plugin) windowHidden() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
	p.rootActive = false
	clear(p.views)
	p.fg = nil
	if p.closed || p.idle != nil {
		return
	}
	idle := make(chan struct{})
	p.idle = idle
	p.wg.Add(1)
	p.h.Go(func() {
		defer p.wg.Done()
		select {
		case <-idle:
		case <-p.opts.After(keepAfterHide):
			p.mu.Lock()
			if p.idle == idle {
				p.idle = nil
				p.st = sampleState{}
				p.samples = 0
			}
			p.mu.Unlock()
		}
	})
}

// cancelIdleLocked stops the release timer. p.mu must be held.
func (p *Plugin) cancelIdleLocked() {
	if p.idle != nil {
		close(p.idle)
		p.idle = nil
	}
}

func (p *Plugin) windowShown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelIdleLocked()
}

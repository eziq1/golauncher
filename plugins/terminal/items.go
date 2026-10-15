package terminal

import (
	"strconv"
	"strings"

	"github.com/eziq1/golauncher/internal/winapi/cmdline"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// sourceWSL is the generator of Windows Terminal's WSL profiles.
const sourceWSL = "Windows.Terminal.Wsl"

// sep separates the parts of a subtitle.
const sep = " · "

// buildItems returns the root items: the profiles, the distributions and
// the WSL commands. folder is the folder of the context ("" for none); the
// "open here" actions are offered only with one.
func buildItems(l plugin.Localizer, s snapshot, folder string) []plugin.Item {
	var items []plugin.Item
	distros := map[string]bool{}
	for _, d := range s.reg.Distros {
		distros[strings.ToLower(d.Name)] = true
	}
	if s.hasWT {
		// Profiles with the same name are opened by GUID: wt -p NAME would
		// always open the first.
		count := map[string]int{}
		for _, pr := range s.term.Profiles {
			count[strings.ToLower(pr.Name)]++
		}
		for _, pr := range s.term.Profiles {
			// The item of a distribution opens its generated profile (from WSL
			// or the distribution's app); do not list it twice.
			if pr.Source != "" && distros[strings.ToLower(pr.Name)] {
				continue
			}
			ref := pr.Name
			if count[strings.ToLower(pr.Name)] > 1 && pr.GUID != "" {
				ref = pr.GUID
			}
			items = append(items, profileItem(l, s.wt, pr, ref, folder))
		}
	}
	if len(s.reg.Distros) > 0 {
		def := s.reg.DefaultName()
		for _, d := range s.reg.Distros {
			st, known := s.states[d.Name]
			items = append(items, distroItem(l, s, d, def, st, known, folder))
		}
		items = append(items, wslCommands(l)...)
	}
	return items
}

// wtArgs is the argument string of wt.exe opening profile ref: in a new
// window, or as a new tab of the most recent window, optionally in dir.
func wtArgs(ref string, newTab bool, dir string) string {
	args := []string{"-w", "new", "-p", ref}
	if newTab {
		args = []string{"-w", "0", "nt", "-p", ref}
	}
	if dir != "" {
		args = append(args, "-d", dir)
	}
	return cmdline.JoinWT(args...)
}

func open(id, title, shortcut, target string, opts plugin.OpenOptions) plugin.Action {
	a := plugin.Action{ID: id, Title: title, Shortcut: shortcut, Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: target}}
	if opts != (plugin.OpenOptions{}) {
		a.Do.Payload = plugin.MustPayload(opts)
	}
	return a
}

// keywords splits a comma-separated message into search keywords.
func keywords(list string) []string {
	var out []string
	for w := range strings.SplitSeq(list, ",") {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// profileItem is the item of a Windows Terminal profile, opened as ref (its
// name, or its GUID when the name is taken twice).
func profileItem(l plugin.Localizer, wt string, pr Profile, ref, folder string) plugin.Item {
	sub := l.T("profile.subtitle")
	if pr.Commandline != "" {
		sub += sep + pr.Commandline
	}
	id := pr.GUID
	if id == "" {
		id = strings.ToLower(pr.Name)
	}
	actions := []plugin.Action{
		open("open", l.T("action.openWindow"), "", wt, plugin.OpenOptions{Args: wtArgs(ref, false, "")}),
		open("new-tab", l.T("action.openTab"), "Ctrl+Enter", wt, plugin.OpenOptions{Args: wtArgs(ref, true, "")}),
		{ID: "admin", Title: l.T("action.openAdmin"), Shortcut: "Ctrl+Shift+Enter", Style: plugin.StyleDestructive,
			Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: wt,
				Payload: plugin.MustPayload(plugin.OpenOptions{Args: wtArgs(ref, false, ""), Verb: "runas"})}},
	}
	if folder != "" {
		actions = append(actions, open("open-here", l.T("action.openHere", "folder", folder), "", wt,
			plugin.OpenOptions{Args: wtArgs(ref, false, folder), Dir: folder}))
	}
	if pr.Commandline != "" {
		actions = append(actions, plugin.Action{ID: "copy", Title: l.T("action.copyCommandline"), Shortcut: "Ctrl+Shift+C",
			Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: pr.Commandline}})
	}
	it := plugin.Item{
		ID:       "profile:" + id,
		Title:    pr.Name,
		Subtitle: sub,
		Icon:     plugin.Icon{Path: pr.Icon, Name: "terminal"},
		Keywords: keywords(l.T("keywords.profile")),
		Group:    l.T("group"),
		Actions:  actions,
	}
	if pr.Default {
		it.Accessory = l.T("accessory.default")
	}
	return it
}

// distroItem is the item of a WSL distribution.
func distroItem(l plugin.Localizer, s snapshot, d Distro, def string, st State, known bool, folder string) plugin.Item {
	version := d.Version
	if version == 0 {
		version = st.Version
	}
	parts := []string{l.T("distro.version", "version", strconv.Itoa(max(version, 1)))}
	kind := stateUnknown
	if known {
		kind = st.kind()
		switch kind {
		case stateRunning:
			parts = append(parts, l.T("state.running"))
		case stateStopped:
			parts = append(parts, l.T("state.stopped"))
		default:
			parts = append(parts, st.Text)
		}
	}
	payload := plugin.MustPayload(callbackPayload{Name: d.Name})

	// Open: through the Windows Terminal profile of the distribution when
	// there is one, else wsl.exe in the home folder.
	var actions []plugin.Action
	if ref := wslProfile(s, d.Name); ref != "" {
		actions = append(actions,
			open("open", l.T("action.open"), "", s.wt, plugin.OpenOptions{Args: wtArgs(ref, false, "")}),
			open("new-tab", l.T("action.openTab"), "Ctrl+Enter", s.wt, plugin.OpenOptions{Args: wtArgs(ref, true, "")}))
	} else {
		actions = append(actions, open("open", l.T("action.open"), "", wslExe, plugin.OpenOptions{Args: cmdline.Join("-d", d.Name, "--cd", "~")}))
	}
	if folder != "" {
		actions = append(actions, open("open-here", l.T("action.openHere", "folder", folder), "", wslExe,
			plugin.OpenOptions{Args: cmdline.Join("-d", d.Name, "--cd", folder), Dir: folder}))
	}
	actions = append(actions, open("explorer", l.T("action.explorer"), "Ctrl+Shift+F", `\\wsl.localhost\`+d.Name, plugin.OpenOptions{}))
	if !strings.EqualFold(d.Name, def) {
		actions = append(actions, plugin.Action{ID: "set-default", Title: l.T("action.setDefault"),
			Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: cbSetDefault, Payload: payload}})
	}
	if kind != stateStopped {
		actions = append(actions, plugin.Action{ID: "terminate", Title: l.T("action.terminate"), Shortcut: "Ctrl+X",
			Style: plugin.StyleDestructive,
			Do:    plugin.ActionDo{Kind: plugin.ActionCallback, Target: cbTerminate, Payload: payload}})
	}
	actions = append(actions, plugin.Action{ID: "copy-name", Title: l.T("action.copyName"), Shortcut: "Ctrl+Shift+C",
		Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: d.Name}})

	icon := plugin.Icon{Name: "square-terminal"}
	if kind == stateRunning {
		icon.Tint = plugin.TintSuccess
	}
	it := plugin.Item{
		ID:       "distro:" + strings.ToLower(d.Name),
		Title:    d.Name,
		Subtitle: strings.Join(parts, sep),
		Icon:     icon,
		Keywords: keywords(l.T("keywords.distro")),
		Group:    l.T("group"),
		Actions:  actions,
		Detail: &plugin.Detail{Metadata: []plugin.Metadata{
			{Label: l.T("meta.location"), Value: d.BasePath},
		}},
	}
	if d.BasePath == "" {
		it.Detail = nil
	}
	if strings.EqualFold(d.Name, def) {
		it.Accessory = l.T("accessory.default")
	}
	return it
}

// wslProfile returns how to open the Windows Terminal profile named like
// distribution name ("" when there is none or no Windows Terminal).
func wslProfile(s snapshot, name string) string {
	if !s.hasWT {
		return ""
	}
	n := 0
	ref := ""
	for _, pr := range s.term.Profiles {
		if strings.EqualFold(pr.Name, name) {
			n++
			// Prefer the WSL generator's own profile.
			if ref == "" || strings.EqualFold(pr.Source, sourceWSL) {
				ref = pr.GUID
			}
		}
	}
	switch {
	case n == 0:
		return ""
	case n == 1 || ref == "":
		return name
	}
	return ref
}

// wslCommands are the WSL commands: shut down (asks first) and update (a
// background task, docs/background-tasks.md).
func wslCommands(l plugin.Localizer) []plugin.Item {
	update := plugin.RunProcessPayload{
		Spec: plugin.TaskSpec{Title: l.T("task.update"), Icon: plugin.Icon{Name: "circle-arrow-down"}, Queue: "wsl", Notify: plugin.NotifyAlways},
		Cmd:  plugin.ProcessSpec{Argv: []string{wslExe, "--update"}},
	}
	return []plugin.Item{
		{
			ID: "wsl-shutdown", Title: l.T("item.shutdown.title"), Subtitle: "wsl --shutdown", // lint:allow-literal
			Icon: plugin.Icon{Name: "circle-stop", Tint: plugin.TintDanger}, Keywords: keywords(l.T("keywords.shutdown")), Group: l.T("group"),
			Actions: []plugin.Action{{ID: "shutdown", Title: l.T("action.shutdown"), Style: plugin.StyleDestructive,
				Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: cbShutdown}}},
		},
		{
			ID: "wsl-update", Title: l.T("item.update.title"), Subtitle: "wsl --update", // lint:allow-literal
			Icon: plugin.Icon{Name: "circle-arrow-down"}, Keywords: keywords(l.T("keywords.update")), Group: l.T("group"),
			Actions: []plugin.Action{{ID: "update", Title: l.T("action.update"),
				Do: plugin.ActionDo{Kind: plugin.ActionRunProcess, Payload: plugin.MustPayload(update)}}},
		},
	}
}

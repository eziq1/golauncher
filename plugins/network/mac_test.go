package network

import (
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// macFixture is the shared fixture on a pretended Mac whose Wi-Fi name is
// hidden (no Location Services permission).
func macFixture(t *testing.T) *FixtureBackend {
	f := fixture(t)
	f.F.Mac = true
	for i := range f.F.WiFi {
		f.F.WiFi[i].SSID, f.F.WiFi[i].Profile, f.F.WiFi[i].SSIDHidden = "", "", true
	}
	return f
}

func TestMacTasks(t *testing.T) {
	p, h := newPlugin(t, Options{Backend: offline{macFixture(t), t}})
	for _, host := range []string{"example.com", "2001:db8::1"} {
		items := root(t, p, "tracert "+host)
		if _, err := p.HandleAction(t.Context(), action(t, items[0], "tracert").Do); err != nil {
			t.Fatal(err)
		}
	}
	r, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdFlushDNS})
	if err != nil {
		t.Fatal(err)
	}
	if r.Toast == nil || r.Toast.Title != "Flush DNS Cache: requires administrator rights" {
		t.Errorf("flush toast %+v", r.Toast)
	}
	if _, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdRenewIP}); err == nil {
		t.Error("renew IP ran on a Mac")
	}
	h.WaitTasks()
	var got [][]string
	for _, c := range h.Runner.Commands() {
		if c.Elevate {
			t.Errorf("%q asks for UAC elevation", c.Argv)
		}
		got = append(got, c.Argv)
	}
	slices.SortFunc(got, func(a, b []string) int { return strings.Compare(strings.Join(a, " "), strings.Join(b, " ")) })
	want := [][]string{
		adminScriptArgv(macFlushDNS, "golauncher needs administrator rights to flush the DNS cache."),
		{"/usr/sbin/traceroute", "example.com"},
		{"/usr/sbin/traceroute6", "2001:db8::1"},
	}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("commands:\n got %q\nwant %q", got, want)
	}
	// The command and the prompt are arguments of the script, never in it.
	argv := want[0]
	if argv[0] != "/usr/bin/osascript" || strings.Contains(argv[2], "dscacheutil") || !strings.Contains(argv[2], "with administrator privileges") {
		t.Errorf("script argv = %q", argv)
	}
	for _, c := range p.Manifest().Commands {
		if c.Name == cmdRenewIP {
			t.Error("renew-ip in the manifest of a Mac")
		}
	}
}

func TestMacTasksAsRoot(t *testing.T) {
	f := macFixture(t)
	f.F.Elevated = true
	p, h := newPlugin(t, Options{Backend: offline{f, t}})
	r, _ := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdFlushDNS})
	if r.Toast == nil || r.Toast.Title != "Flush DNS Cache: running in the background" {
		t.Errorf("toast %+v", r.Toast)
	}
	h.WaitTasks()
	if cmds := h.Runner.Commands(); len(cmds) != 1 || !slices.Equal(cmds[0].Argv, []string{"/bin/sh", "-c", macFlushDNS}) {
		t.Errorf("commands %+v", cmds)
	}
}

func TestMacHiddenSSID(t *testing.T) {
	p, _ := newPlugin(t, Options{Backend: macFixture(t), Sync: true})
	var wifi plugin.Item
	for _, it := range root(t, p, "ip") {
		if it.Icon.Name == "wifi" {
			wifi = it
		}
	}
	if !strings.HasPrefix(wifi.Subtitle, "Wi-Fi name hidden by macOS · ") {
		t.Errorf("subtitle = %q", wifi.Subtitle)
	}
	i := slices.IndexFunc(wifi.Detail.Metadata, func(m plugin.Metadata) bool { return m.Label == "Wi-Fi network" })
	if i < 0 || !strings.Contains(wifi.Detail.Metadata[i].Value, "Location Services") {
		t.Errorf("detail = %+v", wifi.Detail.Metadata)
	}
}

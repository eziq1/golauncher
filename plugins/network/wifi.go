package network

import (
	"context"
	"errors"
	"slices"
	"strings"

	winnet "github.com/eziq1/golauncher/internal/winapi/network"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Saved Wi-Fi networks. A password is read only when the user runs "Copy
// Password"; it goes to the clipboard as a secret (kept out of clipboard
// history and cloud sync) and is never shown or logged.

// wifiPayload is the payload of the copy password callback.
type wifiPayload struct {
	Interface string `json:"interface"`
	Profile   string `json:"profile"`
}

// wifiItems are the saved networks matching filter, the connected ones
// first.
func (p *Plugin) wifiItems(ctx context.Context, filter string, root bool) []plugin.Item {
	l := p.h.I18n()
	profiles, err := p.b.WiFiProfiles(ctx)
	if errors.Is(err, winnet.ErrNoWiFi) {
		return []plugin.Item{{ID: "wifi-none", Title: l.T("wifi.none"), Subtitle: l.T("wifi.noneSubtitle"),
			Icon: plugin.Icon{Name: "wifi", Tint: plugin.TintMuted}}}
	}
	if err != nil {
		return []plugin.Item{{ID: "wifi-error", Title: l.T("wifi.failedList"), Subtitle: err.Error(),
			Icon: plugin.Icon{Name: "alert", Tint: plugin.TintDanger}}}
	}
	conns, _ := p.b.WiFiConnections(ctx)
	connected := func(pr winnet.WiFiProfile) *winnet.WiFiConnection {
		for i := range conns {
			if conns[i].Profile == pr.Name && strings.EqualFold(conns[i].InterfaceGUID, pr.InterfaceGUID) {
				return &conns[i]
			}
		}
		return nil
	}
	var items, rest []plugin.Item
	for _, pr := range profiles {
		if !matches(filter, pr.Name) {
			continue
		}
		it := plugin.Item{
			ID:       "wifi:" + pr.InterfaceGUID + ":" + pr.Name,
			Title:    pr.Name,
			Subtitle: l.T("wifi.saved"),
			Icon:     plugin.Icon{Name: "wifi"},
			Actions: []plugin.Action{
				callback("copy-password", l.T("action.copyPassword"), cbWiFiCopy, wifiPayload{Interface: pr.InterfaceGUID, Profile: pr.Name}, ""),
				copyAction("copy-name", l.T("action.copyName"), pr.Name, "Ctrl+Enter"),
			},
		}
		if root {
			it.Actions = append(it.Actions, push("open", l.T("action.openWiFiView"), toolWiFi, nil, ""))
		}
		if c := connected(pr); c != nil {
			it.Subtitle = l.T("wifi.connected", "signal", c.Signal)
			it.Accessory = l.T("wifi.connectedShort")
			it.Icon.Tint = plugin.TintAccent
			items = append(items, it)
		} else {
			rest = append(rest, it)
		}
	}
	items = append(items, rest...)
	if len(items) == 0 && filter != "" && root {
		return []plugin.Item{{ID: "wifi-nomatch", Title: l.T("wifi.noMatch", "text", filter),
			Icon: plugin.Icon{Name: "wifi", Tint: plugin.TintMuted}}}
	}
	return items
}

// wifiRoot is the root search for the wifi prefix.
func (p *Plugin) wifiRoot(ctx context.Context, rest string) []plugin.Item {
	ctx, cancel := context.WithTimeout(ctx, rootTimeout)
	defer cancel()
	return p.wifiItems(ctx, rest, true)
}

// wifiView lists the saved networks.
func (p *Plugin) wifiView(ctx context.Context) plugin.View {
	l := p.h.I18n()
	return plugin.View{Kind: plugin.ViewList, Title: l.T("view.wifi.title"), Placeholder: l.T("view.wifi.placeholder"),
		EmptyText: l.T("view.wifi.empty"), FilterLocally: true,
		Sections: []plugin.Section{{Items: slices.Clip(p.wifiItems(ctx, "", false))}}}
}

// copyWiFiPassword reads the key of a profile and copies it. The toast
// names the network, never the key.
func (p *Plugin) copyWiFiPassword(ctx context.Context, pl wifiPayload) (plugin.Result, error) {
	l := p.h.I18n()
	k, err := p.b.WiFiKey(ctx, pl.Interface, pl.Profile)
	switch {
	case errors.Is(err, winnet.ErrNoPassword):
		msg := l.T("wifi.noKey", "name", pl.Profile)
		if k.Open() {
			msg = l.T("wifi.open", "name", pl.Profile)
		}
		return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: msg}}, nil
	case errors.Is(err, winnet.ErrNeedsAdmin):
		return failure(l.T("wifi.needsAdmin"), l.T("wifi.needsAdminMessage")), nil
	case errors.Is(err, winnet.ErrNotFound):
		return failure(l.T("wifi.notFound", "name", pl.Profile), ""), nil
	case err != nil:
		return failure(l.T("wifi.failed", "name", pl.Profile), err.Error()), nil
	}
	if err := p.h.Clipboard().WriteSecret(k.Key); err != nil {
		return plugin.Result{}, err
	}
	return plugin.Result{Hide: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("wifi.copied", "name", pl.Profile)}}, nil
}

package calc

import (
	"strings"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

func selItems(t *testing.T, p *Plugin, text string, editable bool, typed string) []plugin.Item {
	t.Helper()
	items, err := p.SelectionItems(t.Context(), plugin.SelectionQuery{
		Selection: plugin.Selection{Text: text, App: plugin.AppInfo{Name: "notepad.exe"}, Source: plugin.SelectionUIA, Editable: editable},
		Text:      typed,
	})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func actionIDs(it plugin.Item) []string {
	var ids []string
	for _, a := range it.Actions {
		ids = append(ids, a.ID)
	}
	return ids
}

func TestSelectionItems(t *testing.T) {
	p, _ := newPluginWith(t, Options{Rates: testRates})
	// A calculation: the root's result item, without paste when the
	// selection is not editable.
	items := selItems(t, p, "  1234 * 5\n", false, "")
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	it := items[0]
	if it.ID != "result" || it.Title != "6,170" || it.Score != 1 || strings.Join(actionIDs(it), ",") != "copy,copy-expression" {
		t.Errorf("item = %+v", it)
	}
	// Editable: paste replaces the selection.
	it = selItems(t, p, "1234 * 5", true, "")[0]
	if ids := actionIDs(it); strings.Join(ids, ",") != "copy,copy-expression,paste" {
		t.Fatalf("editable actions = %q", ids)
	}
	if a := it.Actions[2]; a.Title != "Paste to Replace" || a.Shortcut != "Ctrl+Shift+V" || a.Do.Kind != plugin.ActionPaste || a.Do.Target != "6170" {
		t.Errorf("paste action = %+v", a)
	}
	if it.Actions[0].Title != "Copy Result" {
		t.Errorf("primary action = %+v", it.Actions[0])
	}
	// Typed text: no score, the host matches.
	if it := selItems(t, p, "1234 * 5", false, "copy")[0]; it.Score != 0 {
		t.Errorf("score with typed text = %v", it.Score)
	}

	// Units, timestamps and colors.
	items = selItems(t, p, "10 km in mi", true, "")
	if len(items) != 1 || items[0].ID != "convert:mi" || items[0].Title != "6.21371 mi" || items[0].Score != 1 {
		t.Fatalf("unit = %+v", items)
	}
	if ids := strings.Join(actionIDs(items[0]), ","); ids != "copy,copy-unit,paste,conversions" {
		t.Errorf("unit actions = %s", ids)
	}
	items = selItems(t, p, "10 km", false, "")
	if len(items) < 2 || items[0].Score <= items[1].Score || items[1].Score <= 0 {
		t.Errorf("listing = %+v", titles(items))
	}
	if items := selItems(t, p, "1700000000 to date", false, ""); len(items) == 0 || items[0].Title != "2023-11-14 22:13:20" {
		t.Errorf("timestamp = %q", titles(items))
	}
	if items := selItems(t, p, "#ff8800", false, ""); len(items) == 0 || items[0].Title != "rgb(255, 136, 0)" {
		t.Errorf("color = %q", titles(items))
	}

	// Not calculations.
	for _, in := range []string{"42", "  42  ", "hello world", "1+1\n2+2", strings.Repeat("1+", 600) + "1", "", "pi"} {
		if items := selItems(t, p, in, true, ""); len(items) != 0 {
			t.Errorf("%.30q gave %q", in, titles(items))
		}
	}
}

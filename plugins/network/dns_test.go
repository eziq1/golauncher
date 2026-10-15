package network

import (
	"encoding/binary"
	"net"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// DNS record types.
const (
	typeA     = 1
	typeNS    = 2
	typeCNAME = 5
	typePTR   = 12
	typeMX    = 15
	typeTXT   = 16
	typeAAAA  = 28
)

type stubRR struct {
	typ  uint16
	data []byte
}

func encName(s string) []byte {
	var b []byte
	for l := range strings.SplitSeq(strings.TrimSuffix(s, "."), ".") {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	return append(b, 0)
}

func rrA(s string) stubRR {
	a := netip.MustParseAddr(s)
	if a.Is4() {
		return stubRR{typeA, a.AsSlice()}
	}
	return stubRR{typeAAAA, a.AsSlice()}
}
func rrName(typ uint16, name string) stubRR { return stubRR{typ, encName(name)} }
func rrMX(pref uint16, host string) stubRR {
	return stubRR{typeMX, append(binary.BigEndian.AppendUint16(nil, pref), encName(host)...)}
}
func rrTXT(s string) stubRR { return stubRR{typeTXT, append([]byte{byte(len(s))}, s...)} }

// dnsStub serves zone (names without the final dot) over UDP on
// 127.0.0.1 and returns its address. Unknown names are NXDOMAIN.
func dnsStub(t *testing.T, zone map[string][]stubRR) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if resp := answer(buf[:n], zone); resp != nil {
				pc.WriteTo(resp, from)
			}
		}
	}()
	return pc.LocalAddr().String()
}

// answer builds the response to query q.
func answer(q []byte, zone map[string][]stubRR) []byte {
	if len(q) < 12 {
		return nil
	}
	i := 12
	var labels []string
	for i < len(q) && q[i] != 0 {
		l := int(q[i])
		if i+1+l > len(q) {
			return nil
		}
		labels = append(labels, string(q[i+1:i+1+l]))
		i += 1 + l
	}
	i++ // the root label
	if i+4 > len(q) {
		return nil
	}
	qtype := binary.BigEndian.Uint16(q[i:])
	question := q[12 : i+4]
	name := strings.ToLower(strings.Join(labels, "."))
	rrs, known := zone[name]
	var answers []stubRR
	for _, rr := range rrs {
		if rr.typ == qtype {
			answers = append(answers, rr)
		}
	}
	flags := uint16(0x8180) // response, recursion desired and available
	if !known {
		flags |= 3 // NXDOMAIN
	}
	b := append([]byte{}, q[0], q[1])
	b = binary.BigEndian.AppendUint16(b, flags)
	b = binary.BigEndian.AppendUint16(b, 1)
	b = binary.BigEndian.AppendUint16(b, uint16(len(answers)))
	b = append(b, 0, 0, 0, 0)
	b = append(b, question...)
	for _, rr := range answers {
		b = append(b, 0xc0, 12) // the question's name
		b = binary.BigEndian.AppendUint16(b, rr.typ)
		b = binary.BigEndian.AppendUint16(b, 1)
		b = binary.BigEndian.AppendUint32(b, 60)
		b = binary.BigEndian.AppendUint16(b, uint16(len(rr.data)))
		b = append(b, rr.data...)
	}
	return b
}

func testZone() map[string][]stubRR {
	return map[string][]stubRR{
		"example.test": {rrA("192.0.2.10"), rrA("2001:db8::10"), rrMX(10, "mail.example.test"), rrTXT("v=spf1 -all"),
			rrName(typeNS, "ns1.example.test"), rrName(typeNS, "ns2.example.test")},
		"www.example.test":        {rrName(typeCNAME, "example.test")},
		"10.2.0.192.in-addr.arpa": {rrName(typePTR, "host.example.test")},
	}
}

// TestLookupDNSStub queries every record type at a local DNS server.
func TestLookupDNSStub(t *testing.T) {
	server := dnsStub(t, testZone())
	s := &System{}
	for typ, want := range map[string][]string{
		"A":    {"192.0.2.10"},
		"AAAA": {"2001:db8::10"},
		"MX":   {"mail.example.test."},
		"TXT":  {"v=spf1 -all"},
		"NS":   {"ns1.example.test.", "ns2.example.test."},
	} {
		recs, err := s.LookupDNS(t.Context(), "example.test", typ, server)
		if err != nil {
			t.Errorf("%s: %v", typ, err)
			continue
		}
		var got []string
		for _, r := range recs {
			got = append(got, r.Value)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", typ, got, want)
		}
	}
	if recs, err := s.LookupDNS(t.Context(), "example.test", "MX", server); err != nil || recs[0].Pref != 10 {
		t.Errorf("MX = %+v %v", recs, err)
	}
	if recs, err := s.LookupDNS(t.Context(), "www.example.test", "CNAME", server); err != nil || len(recs) != 1 || recs[0].Value != "example.test." {
		t.Errorf("CNAME = %+v %v", recs, err)
	}
	if recs, err := s.LookupDNS(t.Context(), "192.0.2.10", "PTR", server); err != nil || len(recs) != 1 || recs[0].Value != "host.example.test." {
		t.Errorf("PTR = %+v %v", recs, err)
	}
	if _, err := s.LookupDNS(t.Context(), "nowhere.test", "A", server); err != errNotFound {
		t.Errorf("NXDOMAIN: %v", err)
	}
}

// TestDNSView looks a name up from the root search through the view, at a
// local DNS server given with @server.
func TestDNSView(t *testing.T) {
	server := dnsStub(t, testZone())
	p, _ := newPlugin(t, Options{})
	items := root(t, p, "dns example.test @"+server)
	if len(items) != 1 || items[0].Title != "Look up DNS records of example.test" || items[0].Subtitle != "Ask "+server {
		t.Fatalf("root = %+v", items)
	}
	push := action(t, items[0], "lookup").Do
	v, err := p.View(t.Context(), plugin.ViewRequest{ID: push.Target, Payload: push.Payload, Instance: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Title != "DNS: example.test @"+server {
		t.Errorf("title = %q", v.Title)
	}
	var sections []string
	for _, s := range v.Sections {
		sections = append(sections, s.Title+"="+strings.Join(titles(s.Items), ","))
	}
	want := []string{"A=192.0.2.10", "AAAA=2001:db8::10", "MX=mail.example.test.", "TXT=v=spf1 -all", "NS=ns1.example.test.,ns2.example.test."}
	if !slices.Equal(sections, want) {
		t.Errorf("sections = %q", sections)
	}
	if a := action(t, v.Sections[0].Items[0], "copy-all").Do.Target; !strings.Contains(a, "MX\tmail.example.test.") {
		t.Errorf("copy all = %q", a)
	}
	v, _ = p.View(t.Context(), plugin.ViewRequest{ID: toolDNS, Arguments: map[string]string{"query": "nowhere.test @" + server}})
	if len(v.Sections) != 1 || v.Sections[0].Items[0].Title != "No DNS records for nowhere.test" {
		t.Errorf("nxdomain = %+v", v.Sections)
	}
	v, _ = p.View(t.Context(), plugin.ViewRequest{ID: toolDNS, Arguments: map[string]string{"query": "192.0.2.10 @" + server}})
	if len(v.Sections) != 1 || v.Sections[0].Title != "PTR" || v.Sections[0].Items[0].Title != "host.example.test." {
		t.Errorf("reverse = %+v", v.Sections)
	}
}

// TestDNSServerDown reports a failed lookup per type instead of "no
// records", and does not keep it.
func TestDNSServerDown(t *testing.T) {
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0") // never answers
	defer pc.Close()
	p, _ := newPlugin(t, Options{})
	ctx := t.Context()
	q := dnsQuery{Name: "example.test", Server: pc.LocalAddr().String()}
	v, err := p.dnsView(ctx, plugin.ViewRequest{ID: toolDNS, Payload: plugin.MustPayload(q)})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Sections) != len(forwardTypes) || v.Sections[0].Items[0].Title != "The A lookup failed" {
		t.Errorf("sections = %+v", v.Sections)
	}
	if _, ok := p.dns.get(q.String(), p.h.Now()); ok {
		t.Error("a failed lookup was cached")
	}
}

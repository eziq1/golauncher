package network

import (
	"encoding/binary"
	"net/netip"
	"slices"
	"testing"
)

const profilePSK = `<?xml version="1.0"?>
<WLANProfile xmlns="http://www.microsoft.com/networking/WLAN/profile/v1">
	<name>Home Net</name>
	<SSIDConfig><SSID><hex>486F6D65</hex><name>HomeSSID</name></SSID></SSIDConfig>
	<connectionType>ESS</connectionType>
	<MSM><security>
		<authEncryption><authentication>WPA2PSK</authentication><encryption>AES</encryption><useOneX>false</useOneX></authEncryption>
		<sharedKey><keyType>passPhrase</keyType><protected>false</protected><keyMaterial>fixture-key</keyMaterial></sharedKey>
	</security></MSM>
</WLANProfile>`

const profileProtected = `<WLANProfile xmlns="http://www.microsoft.com/networking/WLAN/profile/v1"><name>Office</name>
<MSM><security><authEncryption><authentication>WPA2PSK</authentication><encryption>AES</encryption></authEncryption>
<sharedKey><keyType>passPhrase</keyType><protected>true</protected><keyMaterial>01000000D08C9DDF</keyMaterial></sharedKey></security></MSM></WLANProfile>`

const profileOpen = `<WLANProfile xmlns="http://www.microsoft.com/networking/WLAN/profile/v1"><name>Cafe</name>
<MSM><security><authEncryption><authentication>open</authentication><encryption>none</encryption></authEncryption></security></MSM></WLANProfile>`

func TestParseProfileXML(t *testing.T) {
	name, k, err := ParseProfileXML([]byte(profilePSK))
	if err != nil {
		t.Fatal(err)
	}
	if name != "Home Net" || k.SSID != "HomeSSID" || k.Authentication != "WPA2PSK" || !k.Plaintext() || k.Key != "fixture-key" || k.Open() {
		t.Errorf("psk: %q %+v", name, k)
	}
	_, k, _ = ParseProfileXML([]byte(profileProtected))
	if k.Plaintext() || !k.Protected || k.Open() {
		t.Errorf("protected: %+v", k)
	}
	_, k, _ = ParseProfileXML([]byte(profileOpen))
	if !k.Open() || k.Plaintext() {
		t.Errorf("open: %+v", k)
	}
	if _, _, err := ParseProfileXML([]byte("<nope")); err == nil {
		t.Error("bad XML parsed")
	}
}

// mibTable builds a MIB table with rows filled by fill.
func mibTable(size int, fill ...func(r []byte)) []byte {
	b := make([]byte, 4+size*len(fill))
	binary.LittleEndian.PutUint32(b, uint32(len(fill)))
	for i, f := range fill {
		f(b[4+i*size : 4+(i+1)*size])
	}
	return b
}

func putPort(b []byte, p uint16) { binary.BigEndian.PutUint16(b, p) }

func TestParseTables(t *testing.T) {
	tcp4 := mibTable(tcp4RowSize,
		func(r []byte) { // listener on 0.0.0.0:8080, pid 1234
			binary.LittleEndian.PutUint32(r, tcpListen)
			putPort(r[8:], 8080)
			binary.LittleEndian.PutUint32(r[20:], 1234)
		},
		func(r []byte) { // established: skipped
			binary.LittleEndian.PutUint32(r, 5)
			putPort(r[8:], 50000)
		},
	)
	if got := ParseTCP4Table(tcp4); !slices.Equal(got, []Socket{{"tcp", netip.MustParseAddrPort("0.0.0.0:8080"), 1234}}) {
		t.Errorf("tcp4 = %v", got)
	}
	tcp6 := mibTable(tcp6RowSize, func(r []byte) {
		copy(r[0:16], netip.MustParseAddr("::1").AsSlice())
		putPort(r[20:], 443)
		binary.LittleEndian.PutUint32(r[48:], tcpListen)
		binary.LittleEndian.PutUint32(r[52:], 7)
	})
	if got := ParseTCP6Table(tcp6); !slices.Equal(got, []Socket{{"tcp", netip.MustParseAddrPort("[::1]:443"), 7}}) {
		t.Errorf("tcp6 = %v", got)
	}
	udp4 := mibTable(udp4RowSize, func(r []byte) {
		copy(r[0:4], []byte{127, 0, 0, 1})
		putPort(r[4:], 53)
		binary.LittleEndian.PutUint32(r[8:], 99)
	})
	if got := ParseUDP4Table(udp4); !slices.Equal(got, []Socket{{"udp", netip.MustParseAddrPort("127.0.0.1:53"), 99}}) {
		t.Errorf("udp4 = %v", got)
	}
	udp6 := mibTable(udp6RowSize, func(r []byte) {
		putPort(r[20:], 5353)
		binary.LittleEndian.PutUint32(r[24:], 3)
	})
	if got := ParseUDP6Table(udp6); !slices.Equal(got, []Socket{{"udp", netip.MustParseAddrPort("[::]:5353"), 3}}) {
		t.Errorf("udp6 = %v", got)
	}
	// A count larger than the buffer is cut to the rows present.
	short := tcp4[:4+tcp4RowSize]
	if got := ParseTCP4Table(short); len(got) != 1 {
		t.Errorf("short table = %v", got)
	}
	if got := ParseUDP4Table(nil); got != nil {
		t.Errorf("empty = %v", got)
	}
}

func TestFormatMAC(t *testing.T) {
	if got := FormatMAC([]byte{0, 0x1a, 0xff}); got != "00-1A-FF" {
		t.Errorf("got %q", got)
	}
}

func TestNormalize(t *testing.T) {
	if Normalize(statusIPv6Unreachable) != StatusHostUnreachable || Normalize(StatusTimedOut) != StatusTimedOut {
		t.Error("normalize")
	}
}

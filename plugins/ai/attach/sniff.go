package attach

import (
	"bytes"
	"encoding/binary"
	"slices"
	"strings"
)

// Sniff classifies a file by its content: head is its beginning (at least
// 512 bytes when the file is that long; it may stop in the middle of a
// character), name its name, used only as a tie breaker (a ZIP named .docx is
// an Office document). It returns KindImage with the image's media type
// (image/png, image/jpeg, image/gif, image/webp, image/bmp, image/tiff),
// KindText with "text/plain" when head is text, or "" and the Reason.
func Sniff(head []byte, name string) (kind string, mediaType string, reason Reason) {
	return sniff(head, name, true)
}

// sniff is Sniff; truncated says whether data is only the head of the file
// (else it is all of it).
func sniff(data []byte, name string, truncated bool) (string, string, Reason) {
	if mt, r := sniffMagic(data, name); mt != "" || r != "" {
		if mt != "" {
			return KindImage, mt, ""
		}
		return "", "", r
	}
	if !truncated && (len(data) == 0 || bytes.Equal(data, bomUTF8) ||
		bytes.Equal(data, bomUTF16LE) || bytes.Equal(data, bomUTF16BE)) {
		return "", "", ReasonEmpty
	}
	if _, ok := decodeText(data, truncated); ok {
		return KindText, "text/plain", ""
	}
	// FF FE is a UTF-16 LE BOM as much as a frame header: such a file that
	// is not text is binary.
	if isMPEGAudio(data) && !bytes.HasPrefix(data, bomUTF16LE) {
		return "", "", ReasonAudio
	}
	return "", "", ReasonBinary
}

func at(data []byte, off int, magic string) bool {
	return len(data) >= off+len(magic) && string(data[off:off+len(magic)]) == magic
}

// sniffMagic recognizes a file by its magic bytes: an image's media type, or
// the Reason of another known kind; both "" when unknown.
func sniffMagic(d []byte, name string) (string, Reason) {
	switch {
	case at(d, 0, "\x89PNG\r\n\x1a\n"):
		return "image/png", ""
	case at(d, 0, "\xff\xd8\xff"):
		return "image/jpeg", ""
	case at(d, 0, "GIF87a"), at(d, 0, "GIF89a"):
		return "image/gif", ""
	case at(d, 0, "RIFF") && at(d, 8, "WEBP"):
		return "image/webp", ""
	case at(d, 0, "RIFF") && at(d, 8, "AVI "):
		return "", ReasonVideo
	case at(d, 0, "RIFF") && at(d, 8, "WAVE"):
		return "", ReasonAudio
	case isBMP(d):
		return "image/bmp", ""
	case at(d, 0, "II*\x00"), at(d, 0, "MM\x00*"):
		return "image/tiff", ""
	case at(d, 4, "ftyp"):
		return "", sniffFtyp(d)
	case at(d, 0, "%PDF-"):
		return "", ReasonPDF
	case at(d, 0, "PK\x03\x04"), at(d, 0, "PK\x05\x06"):
		if isOfficeName(name) {
			return "", ReasonOffice
		}
		return "", ReasonArchive
	case at(d, 0, "\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"):
		return "", ReasonOffice
	case at(d, 0, "7z\xbc\xaf\x27\x1c"),
		at(d, 0, "Rar!\x1a\x07"),
		at(d, 0, "\x1f\x8b"),
		at(d, 0, "\xfd7zXZ\x00"),
		at(d, 0, "\x28\xb5\x2f\xfd"),
		at(d, 257, "ustar"),
		isBzip2(d):
		return "", ReasonArchive
	case at(d, 0, "\x1a\x45\xdf\xa3"),
		at(d, 0, "FLV\x01"),
		at(d, 0, "\x00\x00\x01\xba"):
		return "", ReasonVideo
	case isID3(d),
		at(d, 0, "OggS\x00"),
		at(d, 0, "fLaC"),
		at(d, 0, "FORM") && (at(d, 8, "AIFF") || at(d, 8, "AIFC")):
		return "", ReasonAudio
	}
	return "", ""
}

// isBMP: "BM" and a known DIB header size at offset 14.
func isBMP(d []byte) bool {
	if !at(d, 0, "BM") || len(d) < 18 {
		return false
	}
	switch binary.LittleEndian.Uint32(d[14:18]) {
	case 12, 16, 40, 52, 56, 64, 108, 124:
		return true
	}
	return false
}

func isBzip2(d []byte) bool {
	if !at(d, 0, "BZh") || len(d) < 4 || d[3] < '1' || d[3] > '9' {
		return false
	}
	return at(d, 4, "\x31\x41\x59\x26\x53\x59") || at(d, 4, "\x17\x72\x45\x38\x50\x90")
}

func isID3(d []byte) bool {
	return at(d, 0, "ID3") && len(d) >= 5 && d[3] >= 2 && d[3] <= 4 && d[4] != 0xff
}

// isMPEGAudio: an MPEG audio frame header (or an ADTS one) at the start.
func isMPEGAudio(d []byte) bool {
	if len(d) < 4 || d[0] != 0xff || d[1]&0xe0 != 0xe0 {
		return false
	}
	if d[1]&0xf6 == 0xf0 { // ADTS (AAC): layer bits 00
		return true
	}
	version := (d[1] >> 3) & 3
	layer := (d[1] >> 1) & 3
	bitrate := d[2] >> 4
	rate := (d[2] >> 2) & 3
	return version != 1 && layer != 0 && bitrate != 0xf && rate != 3
}

var officeExts = []string{".docx", ".docm", ".dotx", ".xlsx", ".xlsm", ".xltx", ".pptx", ".pptm", ".potx", ".ppsx", ".odt", ".ods", ".odp", ".odg"}

func isOfficeName(name string) bool {
	name = strings.ToLower(name)
	for _, e := range officeExts {
		if strings.HasSuffix(name, e) {
			return true
		}
	}
	return false
}

var (
	heicBrands  = []string{"heic", "heix", "hevc", "hevx", "heim", "heis", "mif1", "msf1", "avif", "avis"}
	audioBrands = []string{"M4A ", "M4B ", "M4P "}
	videoBrands = []string{"isom", "iso2", "iso4", "iso5", "iso6", "mp41", "mp42", "avc1", "qt  ", "M4V ", "M4VH", "M4VP", "dash", "f4v ", "mmp4", "MSNV"}
)

// sniffFtyp classifies an ISO base media file (an ftyp box at offset 4) by
// its major brand, then its compatible brands; an unknown one is a video.
func sniffFtyp(d []byte) Reason {
	of := func(brand string) Reason {
		switch {
		case slices.Contains(heicBrands, brand):
			return ReasonHEIC
		case slices.Contains(audioBrands, brand):
			return ReasonAudio
		case slices.Contains(videoBrands, brand), strings.HasPrefix(brand, "3g"):
			return ReasonVideo
		}
		return ""
	}
	if len(d) < 12 {
		return ReasonVideo
	}
	if r := of(string(d[8:12])); r != "" {
		return r
	}
	end := len(d)
	if len(d) >= 4 {
		if box := int(binary.BigEndian.Uint32(d[0:4])); box >= 16 && box < end {
			end = box
		}
	}
	for off := 16; off+4 <= end; off += 4 {
		if r := of(string(d[off : off+4])); r != "" {
			return r
		}
	}
	return ReasonVideo
}

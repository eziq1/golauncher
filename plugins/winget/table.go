package winget

import (
	"bytes"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/width"
)

// runeWidth is the number of console cells r takes: two for wide East Asian
// characters, which winget counts the same way when it aligns columns.
func runeWidth(r rune) int {
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	}
	return 1
}

// cells lays s out on console cells: cells[i] is the rune starting at cell
// i, or 0 for the second cell of a wide rune.
func cells(s string) []rune {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		out = append(out, r)
		if runeWidth(r) == 2 {
			out = append(out, 0)
		}
	}
	return out
}

// cellText returns the text of cells [from, to), trimmed.
func cellText(c []rune, from, to int) string {
	if from >= len(c) {
		return ""
	}
	to = min(to, len(c))
	var b strings.Builder
	for _, r := range c[from:to] {
		if r != 0 {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// isSeparator reports whether line is the row of dashes under a table
// header.
func isSeparator(line string) bool {
	line = strings.TrimSpace(line)
	return len(line) >= 3 && strings.Trim(line, "-─") == ""
}

// outputLines splits console output into lines, keeping only the text after
// the last carriage return of each (winget draws spinners and progress bars
// with \r).
func outputLines(out []byte) []string {
	var lines []string
	for line := range bytes.Lines(out) {
		line = bytes.TrimRight(line, "\r\n")
		if i := bytes.LastIndexByte(line, '\r'); i >= 0 {
			line = line[i+1:]
		}
		s := string(line)
		if !utf8.ValidString(s) {
			s = strings.ToValidUTF8(s, "�")
		}
		lines = append(lines, s)
	}
	return lines
}

// parseTable reads the first table in winget's output: a header line, a
// line of dashes, and rows until an empty line. Header texts are localized,
// so columns are found by position: every header word starts a candidate
// column, and a candidate is kept only if the cell before it is blank in
// every row (otherwise it is the second word of a header such as "Id du
// paquet"). Positions are console cells, counting wide characters twice, as
// winget aligns by them. It returns the header and the rows, each with one
// trimmed value per column; ok is false when there is no table.
func parseTable(out []byte) (header []string, rows [][]string, ok bool) {
	lines := outputLines(out)
	sep := -1
	for i := 1; i < len(lines); i++ {
		if isSeparator(lines[i]) && strings.TrimSpace(lines[i-1]) != "" {
			sep = i
			break
		}
	}
	if sep < 0 {
		return nil, nil, false
	}
	head := cells(lines[sep-1])
	// Candidate column starts: a non-blank header cell after a blank one.
	var starts []int
	for i, r := range head {
		if r != ' ' && r != 0 && (i == 0 || head[i-1] == ' ') {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		return nil, nil, false
	}
	// winget pads every row up to the start of the last column, so a
	// shorter line (a summary such as "3 upgrades available.") ends the
	// table, like an empty line. The last column is taken to start at the
	// last candidate after two blanks: a single blank may be inside a
	// localized header of several words.
	last := starts[len(starts)-1]
	for _, s := range slices.Backward(starts) {
		if s >= 2 && head[s-1] == ' ' && head[s-2] == ' ' {
			last = s
			break
		}
	}
	var data [][]rune
	for _, line := range lines[sep+1:] {
		c := cells(line)
		if strings.TrimSpace(line) == "" || len(c) < last {
			break
		}
		data = append(data, c)
	}
	keep := []int{starts[0]}
	for _, s := range starts[1:] {
		good := true
		for _, row := range data {
			if s < len(row) && s > 0 && row[s-1] != ' ' {
				good = false
				break
			}
		}
		if good {
			keep = append(keep, s)
		}
	}
	bounds := append(slices.Clone(keep), 1<<30)
	split := func(c []rune) []string {
		vals := make([]string, len(keep))
		for i := range keep {
			vals[i] = cellText(c, bounds[i], bounds[i+1])
		}
		return vals
	}
	header = split(head)
	for _, row := range data {
		rows = append(rows, split(row))
	}
	return header, rows, true
}

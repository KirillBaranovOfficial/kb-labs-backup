//go:build darwin || linux

package main

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ttyScreenRows = 40
	ttyScreenCols = 120
)

type terminalScreen struct {
	rows      [][]rune
	x         int
	y         int
	savedX    int
	savedY    int
	wrap      bool
	pending   string
	primary   *terminalScreen
	alternate bool
}

func (s *terminalScreen) WriteString(input string) {
	s.Write([]byte(input))
}

func (s *terminalScreen) Write(data []byte) (int, error) {
	input := s.pending + string(data)
	s.pending = ""
	for at := 0; at < len(input); {
		b := input[at]
		if b == 0x1b {
			if at+1 >= len(input) {
				s.pending = input[at:]
				break
			}
			switch input[at+1] {
			case '[':
				end := at + 2
				for end < len(input) && !(input[end] >= 0x40 && input[end] <= 0x7e) {
					end++
				}
				if end == len(input) {
					s.pending = input[at:]
					return len(data), nil
				}
				s.csi(input[at+2:end], input[end])
				at = end + 1
			case ']':
				end := at + 2
				for end < len(input) && input[end] != 0x07 && !(input[end] == 0x1b && end+1 < len(input) && input[end+1] == '\\') {
					end++
				}
				if end == len(input) || input[end] == 0x1b && end+1 == len(input) {
					s.pending = input[at:]
					return len(data), nil
				}
				if input[end] == 0x1b {
					at = end + 2
				} else {
					at = end + 1
				}
			case 'P', '^', '_':
				end := strings.Index(input[at+2:], "\x1b\\")
				if end < 0 {
					s.pending = input[at:]
					return len(data), nil
				}
				at += end + 4
			default:
				s.escape(input[at+1])
				at += 2
			}
			continue
		}
		switch b {
		case '\r':
			s.x, s.wrap = 0, false
			at++
		case '\n':
			s.lineFeed()
			at++
		case '\b':
			if s.x > 0 {
				s.x--
			}
			s.wrap = false
			at++
		case '\t':
			s.x = min(((s.x/8)+1)*8, ttyScreenCols-1)
			s.wrap = false
			at++
		case 0x00, 0x07, 0x0e, 0x0f:
			at++
		default:
			if b < 0x20 {
				at++
				continue
			}
			if !utf8.FullRuneInString(input[at:]) {
				s.pending = input[at:]
				return len(data), nil
			}
			r, size := utf8.DecodeRuneInString(input[at:])
			s.put(r)
			at += size
		}
	}
	return len(data), nil
}

func (s *terminalScreen) ensure() {
	if s.rows != nil {
		return
	}
	s.rows = make([][]rune, ttyScreenRows)
	for row := range s.rows {
		s.rows[row] = make([]rune, ttyScreenCols)
	}
}

func (s *terminalScreen) put(r rune) {
	s.ensure()
	if s.wrap || s.x >= ttyScreenCols {
		s.x = 0
		s.lineFeed()
	}
	s.rows[s.y][s.x] = r
	s.x++
	s.wrap = s.x == ttyScreenCols
}

func (s *terminalScreen) lineFeed() {
	s.ensure()
	s.wrap = false
	if s.y < ttyScreenRows-1 {
		s.y++
		return
	}
	copy(s.rows, s.rows[1:])
	s.rows[ttyScreenRows-1] = make([]rune, ttyScreenCols)
}

func (s *terminalScreen) escape(code byte) {
	s.ensure()
	switch code {
	case '7':
		s.savedX, s.savedY = s.x, s.y
	case '8':
		s.x, s.y = s.savedX, s.savedY
	case 'D':
		s.lineFeed()
	case 'M':
		if s.y > 0 {
			s.y--
		}
	case 'E':
		s.lineFeed()
		s.x = 0
	}
}

func (s *terminalScreen) csi(raw string, final byte) {
	s.ensure()
	private := strings.HasPrefix(raw, "?")
	params := strings.TrimLeft(raw, "? >!")
	values := make([]int, 0, 4)
	for _, token := range strings.Split(params, ";") {
		value, _ := strconv.Atoi(token)
		values = append(values, value)
	}
	param := func(index, fallback int) int {
		if index >= len(values) || values[index] == 0 {
			return fallback
		}
		return values[index]
	}
	switch final {
	case 'A':
		s.y = max(0, s.y-param(0, 1))
	case 'B':
		s.y = min(ttyScreenRows-1, s.y+param(0, 1))
	case 'C':
		s.x = min(ttyScreenCols-1, s.x+param(0, 1))
		s.wrap = false
	case 'D':
		s.x = max(0, s.x-param(0, 1))
		s.wrap = false
	case 'E':
		s.y = min(ttyScreenRows-1, s.y+param(0, 1))
		s.x = 0
	case 'F':
		s.y = max(0, s.y-param(0, 1))
		s.x = 0
	case 'G', '`':
		s.x = min(ttyScreenCols-1, param(0, 1)-1)
		s.wrap = false
	case 'H', 'f':
		s.y = min(ttyScreenRows-1, param(0, 1)-1)
		s.x = min(ttyScreenCols-1, param(1, 1)-1)
		s.wrap = false
	case 'd':
		s.y = min(ttyScreenRows-1, param(0, 1)-1)
	case 'J':
		s.eraseDisplay(param(0, 0))
	case 'K':
		s.eraseLine(param(0, 0))
	case 's':
		s.savedX, s.savedY = s.x, s.y
	case 'u':
		s.x, s.y, s.wrap = s.savedX, s.savedY, false
	case 'h', 'l':
		if private {
			for _, mode := range values {
				if mode == 1049 || mode == 1047 || mode == 47 {
					s.setAlternate(final == 'h')
				}
			}
		}
	}
}

func (s *terminalScreen) eraseLine(mode int) {
	s.ensure()
	switch mode {
	case 0:
		clear(s.rows[s.y][s.x:])
	case 1:
		clear(s.rows[s.y][:s.x+1])
	case 2:
		clear(s.rows[s.y])
	}
}

func (s *terminalScreen) eraseDisplay(mode int) {
	s.ensure()
	switch mode {
	case 0:
		s.eraseLine(0)
		for row := s.y + 1; row < ttyScreenRows; row++ {
			clear(s.rows[row])
		}
	case 1:
		for row := 0; row < s.y; row++ {
			clear(s.rows[row])
		}
		s.eraseLine(1)
	case 2, 3:
		for row := range s.rows {
			clear(s.rows[row])
		}
	}
}

func (s *terminalScreen) setAlternate(enabled bool) {
	if enabled == s.alternate {
		return
	}
	if enabled {
		primary := *s
		primary.primary = nil
		primary.alternate = false
		alt := terminalScreen{primary: &primary, alternate: true}
		alt.ensure()
		*s = alt
		return
	}
	if s.primary != nil {
		primary := *s.primary
		*s = primary
	}
}

func (s *terminalScreen) String() string {
	if s.rows == nil {
		return ""
	}
	last := len(s.rows) - 1
	for last >= 0 && strings.TrimRight(string(s.rows[last]), " \x00") == "" {
		last--
	}
	lines := make([]string, last+1)
	for row := 0; row <= last; row++ {
		lines[row] = strings.TrimRight(string(s.rows[row]), " \x00")
	}
	return strings.Join(lines, "\n")
}

var (
	quotaPercent   = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*%\s*(used|left)?`)
	quotaCount     = regexp.MustCompile(`(?i)([0-9]+)\s*/\s*([0-9]+)\s*(?:requests?|calls?|tokens?)\b`)
	quotaResetIn   = regexp.MustCompile(`(?i)reset(?:s|ting)?\s+in\s+((?:[0-9]+\s*(?:hours?|h|minutes?|m|seconds?|s)\s*)+)`)
	quotaResetPart = regexp.MustCompile(`(?i)([0-9]+)\s*(hours?|h|minutes?|m|seconds?|s)`)
)

func parseQuotaScreen(vendor, rendered string, now func() time.Time) []VendorLimit {
	lines := strings.Split(rendered, "\n")
	limits := make([]VendorLimit, 0, 4)
	for index, line := range lines {
		used, matched := quotaValue(line)
		if !matched {
			continue
		}
		label := quotaWindowLabel(lines, index)
		if label == "" {
			continue
		}
		reset := quotaResetFor(lines, index, now)
		limits = append(limits, VendorLimit{Vendor: vendor, Window: label, UsedPercent: used, ResetsAt: reset, Confidence: "vendor", Source: "tty", FetchedAt: now().UTC()})
	}
	return limits
}

func quotaResetFor(lines []string, index int, now func() time.Time) *time.Time {
	for next := index; next < len(lines); next++ {
		if next > index {
			if _, valueLine := quotaValue(lines[next]); valueLine {
				return nil
			}
		}
		if reset := parseQuotaReset(lines[next], now()); reset != nil {
			return reset
		}
	}
	return nil
}

func quotaValue(line string) (float64, bool) {
	if match := quotaCount.FindStringSubmatch(line); len(match) == 3 {
		used, usedErr := strconv.ParseFloat(match[1], 64)
		maximum, maxErr := strconv.ParseFloat(match[2], 64)
		if usedErr == nil && maxErr == nil && maximum > 0 && used <= maximum {
			return used * 100 / maximum, true
		}
	}
	match := quotaPercent.FindStringSubmatch(line)
	if len(match) != 3 || !strings.Contains(strings.ToLower(line), "usage") && !strings.Contains(strings.ToLower(line), "used") && !strings.Contains(strings.ToLower(line), "left") && !strings.Contains(strings.ToLower(line), "quota") {
		return 0, false
	}
	used, err := strconv.ParseFloat(match[1], 64)
	if err != nil || used > 100 {
		return 0, false
	}
	if strings.EqualFold(match[2], "left") {
		used = 100 - used
	}
	return used, true
}

func quotaWindowLabel(lines []string, index int) string {
	start := max(0, index-3)
	label := strings.ToLower(strings.Join(lines[start:index+1], " "))
	switch {
	case strings.Contains(label, "week"), strings.Contains(label, "7-day"), strings.Contains(label, "7 day"), strings.Contains(label, "7d"):
		return "week"
	case strings.Contains(label, "five-hour"), strings.Contains(label, "five hour"), strings.Contains(label, "5-hour"), strings.Contains(label, "5 hour"), strings.Contains(label, "5h"), strings.Contains(label, "session"):
		return "5h"
	case strings.Contains(label, "daily"), strings.Contains(label, "day"):
		return "daily"
	case strings.Contains(label, "per-model"), strings.Contains(label, "per model"), strings.Contains(label, "model"):
		for prior := index - 1; prior >= start; prior-- {
			line := strings.TrimSpace(lines[prior])
			lower := strings.ToLower(line)
			if strings.Contains(lower, "per-model") || strings.Contains(lower, "per model") || strings.HasPrefix(lower, "model:") {
				_, model, found := strings.Cut(line, ":")
				if found && strings.TrimSpace(model) != "" {
					return "model:" + strings.ToLower(strings.TrimSpace(model))
				}
			}
			if line != "" && !strings.Contains(lower, "model") {
				return "model:" + strings.ToLower(line)
			}
		}
		return "model"
	default:
		return ""
	}
}

func parseQuotaReset(line string, now time.Time) *time.Time {
	if match := quotaResetIn.FindStringSubmatch(line); len(match) == 2 {
		var duration time.Duration
		for _, part := range quotaResetPart.FindAllStringSubmatch(match[1], -1) {
			value, err := strconv.Atoi(part[1])
			if err != nil {
				return nil
			}
			switch strings.ToLower(part[2])[0] {
			case 'h':
				duration += time.Duration(value) * time.Hour
			case 'm':
				duration += time.Duration(value) * time.Minute
			case 's':
				duration += time.Duration(value) * time.Second
			}
		}
		if duration > 0 {
			reset := now.Add(duration).UTC()
			return &reset
		}
	}
	clean := strings.TrimSpace(line)
	for _, prefix := range []string{"resets at ", "reset at ", "resets on ", "reset on ", "resets: ", "reset: ", "resets ", "reset "} {
		if after, ok := strings.CutPrefix(strings.ToLower(clean), prefix); ok {
			clean = strings.TrimSpace(clean[len(clean)-len(after):])
			break
		}
	}
	for _, layout := range []string{time.RFC3339, "Jan 2, 2006 3:04 PM MST", "Jan 2, 2006 3:04 PM", "January 2, 2006 3:04 PM", "Jan 2 2006 15:04 MST", "Jan 2 2006 15:04", "2006-01-02 15:04", "Jan 2 3:04 PM", "January 2 3:04 PM"} {
		reset, err := time.ParseInLocation(layout, clean, now.Location())
		if err == nil {
			if reset.Year() == 0 {
				reset = reset.AddDate(now.Year(), 0, 0)
			}
			if !reset.After(now) && !strings.Contains(clean, strconv.Itoa(now.Year())) {
				reset = reset.AddDate(1, 0, 0)
			}
			reset = reset.UTC()
			return &reset
		}
	}
	return nil
}

type terminalQueryResponder struct{ pending string }

func (r *terminalQueryResponder) Feed(data []byte) (visible []byte, replies [][]byte) {
	r.pending += string(data)
	queries := []struct{ request, response string }{
		{"\x1b[6n", "\x1b[1;1R"},
		{"\x1b[c", "\x1b[?1;2c"},
		{"\x1b]10;?\x07", "\x1b]10;rgb:ffff/ffff/ffff\x07"},
		{"\x1b]11;?\x07", "\x1b]11;rgb:0000/0000/0000\x07"},
		{"\x1b]10;?\x1b\\", "\x1b]10;rgb:ffff/ffff/ffff\x1b\\"},
		{"\x1b]11;?\x1b\\", "\x1b]11;rgb:0000/0000/0000\x1b\\"},
	}
	for {
		earlier := len(r.pending)
		found := -1
		foundAt := len(r.pending)
		for index, query := range queries {
			if at := strings.Index(r.pending, query.request); at >= 0 && at < foundAt {
				found, foundAt = index, at
			}
		}
		if found >= 0 {
			visible = append(visible, r.pending[:foundAt]...)
			replies = append(replies, []byte(queries[found].response))
			r.pending = r.pending[foundAt+len(queries[found].request):]
			continue
		}
		keep := 0
		for _, query := range queries {
			maxPrefix := min(len(r.pending), len(query.request)-1)
			for size := maxPrefix; size > keep; size-- {
				if strings.HasSuffix(r.pending, query.request[:size]) {
					keep = size
					break
				}
			}
		}
		visible = append(visible, r.pending[:len(r.pending)-keep]...)
		r.pending = r.pending[len(r.pending)-keep:]
		if len(r.pending) >= earlier {
			break
		}
		break
	}
	return visible, replies
}

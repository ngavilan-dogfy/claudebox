package main

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

var colorOn = isTTY(os.Stdout) && os.Getenv("NO_COLOR") == ""

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func paint(code, s string) string {
	if !colorOn {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func green(s string) string  { return paint("32", s) }
func yellow(s string) string { return paint("33", s) }
func red(s string) string    { return paint("31", s) }
func cyan(s string) string   { return paint("36", s) }
func dim(s string) string    { return paint("2", s) }
func bold(s string) string   { return paint("1", s) }

func okLine(format string, a ...any)   { fmt.Printf("  %s  %s\n", green("✓"), fmt.Sprintf(format, a...)) }
func warnLine(format string, a ...any) { fmt.Printf("  %s  %s\n", yellow("!"), fmt.Sprintf(format, a...)) }
func badLine(format string, a ...any)  { fmt.Printf("  %s  %s\n", red("✗"), fmt.Sprintf(format, a...)) }

// panel renders a rounded box with aligned key/value rows. Values may carry
// ANSI codes; widths are computed on the plain text.
func panel(title string, rows [][2]string) string {
	keyW, valW := 0, 0
	for _, r := range rows {
		if w := utf8.RuneCountInString(r[0]); w > keyW {
			keyW = w
		}
		if w := utf8.RuneCountInString(stripANSI(r[1])); w > valW {
			valW = w
		}
	}
	inner := keyW + valW + 4 // matches the row layout: space + key + 2 spaces + value + space
	head := "─ " + title + " "
	if pad := inner - utf8.RuneCountInString(head); pad > 0 {
		head += strings.Repeat("─", pad)
	}
	var b strings.Builder
	b.WriteString(dim("╭"+head+"╮") + "\n")
	for _, r := range rows {
		val := r[1] + strings.Repeat(" ", valW-utf8.RuneCountInString(stripANSI(r[1])))
		b.WriteString(dim("│") + " " + dim(pad(r[0], keyW)) + "  " + val + " " + dim("│") + "\n")
	}
	b.WriteString(dim("╰"+strings.Repeat("─", inner)+"╯") + "\n")
	return b.String()
}

func pad(s string, w int) string {
	if n := w - utf8.RuneCountInString(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		case r == '\x1b':
			inEsc = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// lineWriter prefixes every line written through it — used to render
// docker build output as dimmed, indented log lines.
type lineWriter struct {
	prefix string
	buf    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := strings.IndexByte(string(w.buf), '\n')
		if i < 0 {
			break
		}
		fmt.Println(dim(w.prefix + string(w.buf[:i])))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

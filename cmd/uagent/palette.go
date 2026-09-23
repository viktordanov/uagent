package main

import "os"

// Palette adds ANSI colors when enabled.
type Palette struct{ on bool }

// PaletteFor enables color when f is a terminal and NO_COLOR is unset.
func PaletteFor(f *os.File) Palette {
	if os.Getenv("NO_COLOR") != "" {
		return Palette{}
	}
	info, err := f.Stat()

	return Palette{on: err == nil && info.Mode()&os.ModeCharDevice != 0}
}

func (p Palette) wrap(code, s string) string {
	if !p.on {
		return s
	}

	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p Palette) Bold(s string) string   { return p.wrap("1", s) }
func (p Palette) Dim(s string) string    { return p.wrap("2", s) }
func (p Palette) Red(s string) string    { return p.wrap("31", s) }
func (p Palette) Green(s string) string  { return p.wrap("32", s) }
func (p Palette) Yellow(s string) string { return p.wrap("33", s) }
func (p Palette) Cyan(s string) string   { return p.wrap("36", s) }

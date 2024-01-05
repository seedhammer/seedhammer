package text

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"golang.org/x/image/math/fixed"
	"seedhammer.com/font/poppins"
)

func BenchmarkLayout(b *testing.B) {
	bytes := any([]byte{'a', 'b'})
	for range b.N {
		format := "₿ %.2d%% %s %.8x %c %s %.32b"
		args := []any{120, "Hi", 0xcafe, 'B', bytes, 0b11101100}
		l := &Layout{
			MaxWidth: 100,
			Style: Style{
				Face: poppins.Regular16,
			},
		}
		for {
			if _, ok := l.Next(format, args...); !ok {
				break
			}
		}
	}
}

func TestAllocs(t *testing.T) {
	res := testing.Benchmark(BenchmarkLayout)
	allocs := res.AllocsPerOp()
	bytes := res.AllocedBytesPerOp()
	if allocs > 0 || bytes > 0 {
		t.Errorf("Layout allocates %d (%d bytes), expected none", allocs, bytes)
	}
}

func TestLayout(t *testing.T) {
	type line struct {
		str   string
		width int
	}
	tests := []struct {
		format string
		args   []any
		width  int
		want   []line
	}{
		{
			"Hello World", nil, 100,
			[]line{{"Hello World", 90}},
		},
		{
			"%c", []any{'P'}, 100,
			[]line{{"P", 9}},
		},
		{
			"Hello %s", []any{"Format"}, 100,
			[]line{{"Hello Format", 100}},
		},
		{
			"₿ %.2g%% %f %g %.8x %2d %d %c %s", []any{12.345, 12.345, 12.345, 0xcafe, uint64(9), int64(-12), '₿', []byte("abc")}, 1000,
			[]line{{"₿ 12% 12.345000 12.345 0000cafe  9 -12 ₿ abc", 325}},
		},
		{
			"Hello Aligned World", nil, 70,
			[]line{{"Hello", 39}, {"Aligned", 61}, {"World", 47}},
		},
		{
			"Hello\n\nWorld", nil, 70,
			[]line{{"Hello", 39}, {"", 0}, {"World", 47}},
		},
	}
	for _, test := range tests {
		for _, align := range []Alignment{AlignStart, AlignEnd, AlignCenter} {
			var gotLines []line
			var buf strings.Builder
			dot := fixed.I(0)
			first := true
			st := Style{
				Face:      poppins.Regular16,
				Alignment: align,
			}
			adv := fixed.I(0)
			s := fmt.Sprintf(test.format, test.args...)
			endline := func() {
				width := adv.Ceil()
				l := buf.String()
				buf.Reset()
				wantDot := fixed.I(0)
				switch align {
				case AlignCenter:
					wantDot = (fixed.I(test.width) - adv) / 2
				case AlignEnd:
					wantDot = fixed.I(test.width) - adv
				}
				if dot != wantDot {
					t.Errorf("%s: line %q dot %v, want %v", s, l, dot, wantDot)
				}
				gotLines = append(gotLines, line{l, width})
				adv = 0
				first = true
			}
			l := &Layout{
				MaxWidth: test.width,
				Style:    st,
			}
			for {
				g, ok := l.Next(test.format, test.args...)
				if !ok {
					break
				}
				if first {
					dot = g.Dot
					first = false
				}
				if g.Rune == '\n' {
					endline()
					continue
				}
				adv += g.Advance
				buf.WriteRune(g.Rune)
			}
			endline()
			if align == AlignStart {
				if !slices.Equal(gotLines, test.want) {
					t.Errorf("%s: got %v, want %v", s, gotLines, test.want)
				}
			}
		}
	}
}

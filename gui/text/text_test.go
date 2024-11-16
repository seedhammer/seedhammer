package text

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/image/math/fixed"
	"seedhammer.com/font/poppins"
)

func BenchmarkLayout(b *testing.B) {
	st := Style{
		Face: poppins.Regular16,
	}
	for range b.N {
		for range st.Layout(100, "₿ %.2g%% %f %g %s %.8x", 12.345, 12.345, 12.345, "Hi", 0xcafe) {
		}
	}
}

func TestAllocs(t *testing.T) {
	allocs := testing.Benchmark(BenchmarkLayout).AllocsPerOp()
	if allocs > 0 {
		t.Errorf("Layout allocates %d, expected %d", allocs, 0)
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
			"Hello %s", []any{"Format"}, 100,
			[]line{{"Hello Format", 100}},
		},
		{
			"₿ %.2g%% %f %g %.8x %2d", []any{12.345, 12.345, 12.345, 0xcafe, 9}, 1000,
			[]line{{"₿ 12% 12.345000 12.345 0000cafe  9", 258}},
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
			for l := range st.Layout(test.width, test.format, test.args...) {
				if first {
					dot = l.Dot
					first = false
				}
				if l.Rune == '\n' {
					endline()
					continue
				}
				adv += l.Advance
				buf.WriteRune(l.Rune)
			}
			endline()
			if align == AlignStart {
				if !reflect.DeepEqual(gotLines, test.want) {
					t.Errorf("%s: got %v, want %v", s, gotLines, test.want)
				}
			}
		}
	}
}

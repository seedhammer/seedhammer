// package affine implements mathematical operations on the
// golang.org/x/image/math/f32 data types.
package affine

import (
	"image"
	"math"

	"golang.org/x/image/math/f32"
)

func mul(A, B f32.Aff3) (r f32.Aff3) {
	r[0] = A[0]*B[0] + A[1]*B[3]
	r[1] = A[0]*B[1] + A[1]*B[4]
	r[2] = A[0]*B[2] + A[1]*B[5] + A[2]
	r[3] = A[3]*B[0] + A[4]*B[3]
	r[4] = A[3]*B[1] + A[4]*B[4]
	r[5] = A[3]*B[2] + A[4]*B[5] + A[5]
	return r
}

func Scale(p f32.Vec2, s float32) f32.Vec2 {
	return f32.Vec2{p[0] * s, p[1] * s}
}

func Add(p ...f32.Vec2) f32.Vec2 {
	r := p[0]
	for i := 1; i < len(p); i++ {
		r = f32.Vec2{r[0] + p[i][0], r[1] + p[i][1]}
	}
	return r
}

func Sub(p ...f32.Vec2) f32.Vec2 {
	r := p[0]
	for i := 1; i < len(p); i++ {
		r = f32.Vec2{r[0] - p[i][0], r[1] - p[i][1]}
	}
	return r
}

func Pointf(p image.Point) f32.Vec2 {
	return f32.Vec2{float32(math.Round(float64(p.X))), float32(math.Round(float64(p.Y)))}
}

func Dot(p0, p1 f32.Vec2) float32 {
	return p0[0]*p1[0] + p0[1]*p1[1]
}

func Length(p f32.Vec2) float32 {
	return float32(math.Sqrt(float64(Dot(p, p))))
}

func Div(v f32.Vec2, s float32) f32.Vec2 {
	return f32.Vec2{v[0] / s, v[1] / s}
}

func Mul(M ...f32.Aff3) (r f32.Aff3) {
	r = M[0]
	for i := 1; i < len(M); i++ {
		r = mul(r, M[i])
	}
	return r
}

func Offsetting(p f32.Vec2) f32.Aff3 {
	return f32.Aff3{
		1, 0, p[0],
		0, 1, p[1],
	}
}

func Scaling(s f32.Vec2) f32.Aff3 {
	return f32.Aff3{
		s[0], 0, 0,
		0, s[1], 0,
	}
}

func Rotating(radians float32) f32.Aff3 {
	sin, cos := math.Sincos(float64(radians))
	s, c := float32(sin), float32(cos)
	return f32.Aff3{
		c, -s, 0,
		s, c, 0,
	}
}

func Transform(m f32.Aff3, p f32.Vec2) f32.Vec2 {
	return f32.Vec2{
		p[0]*m[0] + p[1]*m[1] + m[2],
		p[0]*m[3] + p[1]*m[4] + m[5],
	}
}

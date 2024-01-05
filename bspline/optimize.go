package bspline

import (
	"fmt"
	"math"

	"gonum.org/v1/gonum/mat"
	"gonum.org/v1/gonum/optimize/convex/lp"
	"seedhammer.com/bezier"
)

// InterpolatePoints takes a clamped cubic uniform b-spline and
// returns a similar spline that minimizes maximum speed,
// acceleration and jerk. The returned spline has a control point
// in each control point interval of the input spline.
// As a special case, a zero-length input B-spline is returned as is.
//
// For example, the clamped uniform B-spline with n+4 control
// points,
//
//	P0P0P0 - P1 - P2 - P3 - … - P{n-1}P{n-1}P{n-1}
//
// is turned into another clamped uniform B-spline n+5 control
// points
//
//	Q0Q0Q0 - Q1 - Q2 - Q3 - Q4 - … - Q{n}Q{n}Q{n}
//
// where
//
//	Q0 = P0,
//	Q{n} = P{n-1}, and
//	Q{i} for i∈[1,n-1] is on the line segment P{i-1} - P{i}.
func InterpolatePoints(pts []bezier.Point) (knots []bezier.Point, err error) {
	knots, _, _, _, err = interpolatePoints(pts)
	return
}

func interpolatePoints(pts []bezier.Point) (knots, v, a, j []bezier.Point, err error) {
	// Find the placement of the knots of the smooth B-spline,
	// whose control points are on the line segments of the original.
	// The placement should minimize the maximum of the kinetic
	// properties velocity, acceleration and jerk.
	//
	// Because of the strong convex hull property of B-splines,
	// and because the derivative of a B-spline is another B-spline,
	// it suffices to minimize the kinetic properties at the control
	// points of the B-splines that corresponds to the kinetic properties.
	//
	// Construct a linear program that minimizes J >= 0, which is the
	// maximum of all properties of all control points of the input
	// B-spline.
	//
	// The program is on the form
	//
	//	minimize	cᵀ x
	//	such that	Ax = b
	//				x >= 0 .
	//
	// To confine the position of internal control points to line
	// segments of the input spline, define each point Q{i} in terms of
	// a scalar weight, w{i}∈]0,1[ such that
	//
	//  Q{i} = (1-w{i})P{i}+w{i}P{i+1}
	//       = w{i}(P{i+1} - P{i}) + P{i}
	//
	// The weight interval is open to ensure unique control points.
	// Any duplicate control point would violate the C² continuity
	// of the cubic B-spline.
	//
	// To clamp the output spline, it must contain 3 duplicate control
	// points at each end. Output control point lie between input points,
	// so the input spline is extended by one point at each end to ensure 4
	// identical start and end points.
	// Since there is an output control point per interval, the output spline
	// contains one control point more than the input.

	const naxis = 2
	// nsegs is the number of segments, which is the number of
	// variable control points.
	nsegs := len(pts) - 1

	// Variables begin at offset 1; offset 0 is for the
	// constants.
	const varOff = 1
	// nctrl is the number of control point variables, one per segment
	// and axis, one λ per segment, and the goal variable J.
	nctrl := nsegs*(naxis+1) + 1
	ctrl := func(axis, i int) expr {
		idx := varOff + axis*nsegs + i
		return varExpr(idx)
	}
	λ := func(i int) expr {
		λOff := varOff + nsegs*naxis
		return varExpr(λOff + i)
	}
	// The goal variable is last.
	jOff := varOff + nctrl - 1
	J := varExpr(jOff)
	// Separating equality constraints seems to help LP
	// robustness.
	var eqs, ineqs []expr
	// addConstraint adds a constraint on the form
	//
	//  expr op 0 where op is '=' or '≤'.
	//
	// It returns an expr for the slack variable, if any.
	addConstraint := func(cons expr, op rune) expr {
		if cons.IsZero() {
			return expr{}
		}
		if op == '≤' {
			slackIdx := jOff + 1 + len(ineqs)
			slack := varExpr(slackIdx)
			cons = cons.Add(slack)
			ineqs = append(ineqs, cons)
			return slack
		}
		eqs = append(eqs, cons)
		return expr{}
	}
	// Add an inequality constraint on the form
	//
	//  |e| <= J
	//
	// It returns an equivalent expression, using
	// the goal and a slack variable.
	addKinematic := func(e expr, scale float64) expr {
		if e.IsZero() {
			return expr{}
		}
		// The absolute function expands to an inequality constraint
		// for each direction, on the standard form
		//
		//  ±e - J <= 0
		//
		s := addConstraint(e.Mul(+scale).Sub(J), '≤')
		addConstraint(e.Mul(-scale).Sub(J), '≤')
		return J.Sub(s).Div(scale)
	}

	// Acceleration and jerk scale non-linearly (squared and cubed) with
	// time scaling. Linear programs don't support non-linear scaling, so
	// instead scale the contributions to the goal.
	const (
		vScale = 1
		aScale = 1
		jScale = 10
	)
	derive := func(knots [4]uint, p0, p1 expr, degree int) expr {
		t := uint(0)
		for _, k := range knots[1 : degree+1] {
			t += k
		}
		res := expr{}
		if t != 0 {
			res = p1.Sub(p0).Mul(float64(degree) / float64(t))
		}
		return res
	}
	type state struct {
		knots [4]uint
		λ     [2]expr
		s     [2]float64
		p     [3]expr
		v, a  expr
	}
	var vExprs, aExprs, jExprs []expr
	nknots := 0
	knot := func(last state, t uint, p expr, s float64, λ expr) state {
		copy(last.knots[:], last.knots[1:])
		last.knots[3] = t
		// Construct geometric constraints such that each knot of the B-spline
		// hit a line segment. The start and end control points do not need
		// constraints because they're fixed.
		//
		// A uniform B-spline at knot t{i} evaluates to
		//
		//  BS(t{i}) = B{i-1}*C{i-1} + B{i}*C{i} + B{i+1}*C{i+1}
		//
		// Where B{i} is the ith basis function of third degree.
		//
		// Knot t{i} must lie in the line segment between S{i} and S{i+1}:
		//
		//  BS(t{i}) = (1-λ{i})*S{i} + λ{i}*S{i+1}, λ{i}∈]0;1[
		//           = S{i} + (S{i+1}-S{i})*λ{i}
		//
		// The λ intervals are open to avoid overlapping control points.
		// Overlapping control points reduce the continuity of B-splines.
		//
		// The combination leads to the constraint,
		//
		//  B{i-1}*C{i-1} + B{i}*C{i} + B{i+1}*C{i+1} = S{i} + (S{i+1}-S{i})*λ{i}
		//
		// On standard form:
		//
		//  B{i-1}*C{i-1} + B{i}*C{i} + B{i+1}*C{i+1} - (S{i+1}-S{i})*λ{i} - S{i} = 0
		B := bsplineBasis(last.knots)
		s0, s1 := last.s[0], last.s[1]
		ctrl := expr{}
		for j, b := range B {
			ctrl = ctrl.Add(last.p[j].Mul(b))
		}
		cons := ctrl.Add(last.λ[0].Mul(-(s1 - s0))).Sub(constExpr(s0))
		addConstraint(cons, '=')

		// Shift control point and waypoint into state.
		copy(last.s[:], last.s[1:])
		last.s[1] = s
		copy(last.p[:], last.p[1:])
		last.p[2] = p
		copy(last.λ[:], last.λ[1:])
		last.λ[1] = λ
		v := derive(last.knots, last.p[0], last.p[1], 3)
		a := derive(last.knots, last.v, v, 2)
		last.v = v
		j := derive(last.knots, last.a, a, 1)
		last.a = a

		// Add kinetic constraints.
		if nknots >= 3 {
			vExprs = append(vExprs, addKinematic(v, vScale))
			if nknots >= 4 {
				aExprs = append(aExprs, addKinematic(a, aScale))
				if nknots >= 5 {
					jExprs = append(jExprs, addKinematic(j, jScale))
				}
			}
		}
		nknots++
		return last
	}
	// Improve the linear program conditioning by normalizing the input points
	// to the [1;2] range. The [0;1] range is left for control points because
	// the standard linear program model forces control coordinates to be
	// non-negative. The alternative is to split control coordinates in two
	// variables per axis.
	var minPt, ptScale [naxis]float64
	const ptOffset = 1
	// For each axis, construct symbolic control points and apply
	// constraints on them.
	for axis := range naxis {
		nknots = 0
		mi, ma := math.Inf(+1), math.Inf(-1)
		for _, p := range pts {
			pf := [naxis]float64{float64(p.X), float64(p.Y)}[axis]
			mi, ma = min(mi, pf), max(ma, pf)
		}
		scale := max(1, ma-mi)
		s := func(i int) float64 {
			p := pts[i]
			pf := [naxis]float64{float64(p.X), float64(p.Y)}[axis]
			return (pf-mi)/scale + ptOffset
		}
		minPt[axis] = mi
		ptScale[axis] = scale
		// state is a sliding window of previous control points and
		// kinetic values.
		var last state
		// Fixed start control points.
		start := s(0)
		for range 3 {
			last = knot(last, 0, constExpr(start), start, expr{})
		}
		// Variable inner control points.
		for i := range nsegs {
			last = knot(last, 1, ctrl(axis, i), s(i), λ(i))
		}
		// Fixed end points.
		end := s(nsegs)
		for i := range 3 {
			t := uint(1)
			if i > 0 {
				t = 0
			}
			last = knot(last, t, constExpr(end), end, expr{})
		}
	}

	// Constrain the weights by expressing w{i}∈]0,1[ in
	// terms of a small constant:
	//
	//  0 <= w{i} <= 1-ε
	//
	// Since the first constraint is implied in an LP, there
	// is only the upper limit:
	//
	//  w{i} <= 1-ε
	//
	const ε = .05
	for i := range nsegs {
		cons := λ(i).Add(constExpr(-(1 - ε)))
		addConstraint(cons, '≤')
	}
	// As an edge case, the first weight must not be
	// zero, otherwise its point may overlap the previous,
	// fixed point. In standard form:
	//
	//  -w{0} <= -ε
	cons := λ(0).Mul(-1).Add(constExpr(ε))
	addConstraint(cons, '≤')
	// Total number of constraints.
	ncons := len(ineqs) + len(eqs)
	// Total number of variables.
	nvars := nctrl + len(ineqs)
	A := mat.NewDense(ncons, nvars, nil)
	b := make([]float64, ncons)
	// Copy constraints to A.
	for row, c := range ineqs {
		cons := c.Explode()
		for j, v := range cons[1:] {
			A.Set(row, j, v)
		}
		b[row] = -cons[0]
	}
	for i, c := range eqs {
		row := len(ineqs) + i
		cons := c.Explode()
		for j, v := range cons[1:] {
			A.Set(row, j, v)
		}
		b[row] = -cons[0]
	}
	// Minimize the goal, J.
	c := make([]float64, nvars)
	copy(c, J.Explode()[1:])
	_, x, err := lp.Simplex(c, A, b, 1e-6, nil)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	// eval evaluates an expression on the LP result.
	eval := func(e expr) float64 {
		coeffs := e.Explode()
		v := coeffs[0]
		for i, coeff := range coeffs[1:] {
			v += coeff * x[i]
		}
		return v
	}
	// Recover internal control points.
	toPoint := func(v [2]float64) bezier.Point {
		return bezier.Point{
			X: int(math.Round(v[0])),
			Y: int(math.Round(v[1])),
		}
	}
	ctrls := make([]bezier.Point, 0, nsegs+2)
	// Clamping start point.
	s, e := pts[0], pts[len(pts)-1]
	ctrls = append(ctrls, s, s, s)
	for i := range nsegs {
		var c [naxis]float64
		for axis := range naxis {
			// Undo robustness transformation.
			v := eval(ctrl(axis, i))
			scale, mi := ptScale[axis], minPt[axis]
			c[axis] = (v-ptOffset)*scale + mi
		}
		ctrls = append(ctrls, toPoint(c))
	}
	// Clamping end points.
	ctrls = append(ctrls, e, e, e)

	// Recover kinematic values.
	recoverKin := func(exprs []expr) []bezier.Point {
		var v []bezier.Point
		nvals := len(exprs) / 2
		for i := range nvals {
			var c [naxis]float64
			for axis := range c {
				scale := ptScale[axis]
				e := exprs[axis*nvals+i]
				c[axis] = eval(e) * scale
			}
			v = append(v, toPoint(c))
		}
		return v
	}
	v = recoverKin(vExprs)
	a = recoverKin(aExprs)
	j = recoverKin(jExprs)
	return ctrls, v, a, j, nil
}

// bsplineBasis computes the coefficients of the B-spline control
// points at the start of the segment.
func bsplineBasis(knots [4]uint) [3]float64 {
	dt1, dt2, dt3, dt4 := float64(knots[0]), float64(knots[1]), float64(knots[2]), float64(knots[3])
	if dt3 == 0 {
		// Zero duration curve.
		return [...]float64{0, 1, 0}
	}
	d1 := dt4 + dt3 + dt2
	p334c2, p334c3 := (dt4+dt3)/d1, dt2/d1
	d2 := dt3 + dt2 + dt1
	p323c1, p323c2 := dt3/d2, (dt2+dt1)/d2
	d4 := dt3 + dt2
	c1, c2, c3 := p323c1*dt3/d4, (p323c2*dt3+p334c2*dt2)/d4, p334c3*dt2/d4
	return [...]float64{c1, c2, c3}
}

// constExpr returns an expression with the constant coefficient
// set to c.
func constExpr(c float64) expr {
	return expr{
		c0: c,
	}
}

// varExpr returns an expression with the ith coefficient set
// to 1.
func varExpr(i int) expr {
	if i == 0 {
		return constExpr(1)
	}
	s := expr{
		zeros: i - 1,
		c:     make([]float64, 1),
	}
	s.c[0] = 1
	return s.normalize()
}

// expr is a value represented by a vector of coefficients
// c{i} for implicit variables v{i}:
//
//	e = c{0} + c{1}v{0}...c{n}v{n-1}
type expr struct {
	// c0 stores c{0}.
	c0 float64
	// c stores the extra coefficients. The layout is
	//
	//  c[i] = c{zeros+i}
	c []float64
	// zeros store the number of implied zero
	// coefficients between c0 and c[0].
	zeros int
}

func (s expr) IsZero() bool {
	return s.c0 == 0 && len(s.c) == 0
}

func (s expr) Explode() []float64 {
	r := make([]float64, s.numCoeffs())
	r[0] = s.c0
	copy(r[1+s.zeros:], s.c)
	return r
}

func (s expr) numCoeffs() int {
	return 1 + s.zeros + len(s.c)
}

func (s expr) String() string {
	coeffs := s.Explode()
	if len(coeffs) == 1 {
		return fmt.Sprintf("%g", coeffs[0])
	}
	return fmt.Sprintf("%g", coeffs)
}

func (s expr) Const() float64 {
	if len(s.c) > 0 {
		panic("non-const expression")
	}
	return s.c0
}

func (s expr) copy() expr {
	c := expr{
		zeros: s.zeros,
		c0:    s.c0,
		c:     make([]float64, len(s.c)),
	}
	copy(c.c, s.c)
	return c
}

func (s expr) Mul(f float64) expr {
	sf := s.copy()
	sf.c0 *= f
	for i := range sf.c {
		sf.c[i] *= f
	}
	return sf.normalize()
}

func (s expr) Div(f float64) expr {
	sf := s.copy()
	sf.c0 /= f
	for i := range sf.c {
		sf.c[i] /= f
	}
	return sf.normalize()
}

func (s expr) Sub(s2 expr) expr {
	return s.Add(s2.Mul(-1))
}

func (s expr) Add(s2 expr) expr {
	cmin := min(s.zeros, s2.zeros)
	cmax := max(s.zeros+len(s.c), s2.zeros+len(s2.c))
	r := expr{
		c0:    s.c0 + s2.c0,
		zeros: cmin,
		c:     make([]float64, cmax-cmin),
	}
	copy(r.c[s.zeros-cmin:], s.c)
	for i, c := range s2.c {
		r.c[s2.zeros-cmin+i] += c
	}
	return r.normalize()
}

// normalize left-adjusts the coefficients.
func (s expr) normalize() expr {
	for len(s.c) > 0 {
		n := len(s.c)
		if s.c[n-1] != 0 {
			break
		}
		s.c = s.c[:n-1]
	}
	for i, c := range s.c {
		if c == 0 {
			continue
		}
		copy(s.c, s.c[i:])
		s.c = s.c[:len(s.c)-i]
		s.zeros += i
		break
	}
	return s
}

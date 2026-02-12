package gui

import (
	"time"

	"seedhammer.com/bspline"
)

type engraveJob struct {
	quit     chan<- struct{}
	progress chan uint
	errs     chan error
	tps      uint

	ticks uint
	done  bool
	err   error
}

func newEngraverJob(p Platform, spline bspline.Curve) *engraveJob {
	errs := make(chan error, 1)
	progress := make(chan uint, 1)
	quit := make(chan struct{})
	e := &engraveJob{
		errs:     errs,
		progress: progress,
		quit:     quit,
		tps:      p.EngraverParams().TicksPerSecond,
	}
	pspline := func(yield func(bspline.Knot) bool) {
		var ticks uint
		for k := range spline {
			if !yield(k) {
				return
			}
			ticks += k.T
			select {
			case <-progress:
			default:
			}
			progress <- ticks
			p.Wakeup()
		}
	}
	go func() {
		defer p.Wakeup()
		errs <- p.Engrave(true, pspline, quit)
	}()
	return e
}

func (e *engraveJob) Remaining() time.Duration {
	select {
	case t := <-e.progress:
		e.ticks = t
	default:
	}
	return time.Duration(e.ticks+e.tps-1) * time.Second / time.Duration(e.tps)
}

func (e *engraveJob) Status() (done bool, err error) {
	select {
	case err := <-e.errs:
		e.err = err
		e.done = true
	default:
	}
	return e.done, e.err
}

func (e *engraveJob) Cancel() {
	close(e.quit)
}

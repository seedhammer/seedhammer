package gui

import (
	"seedhammer.com/bspline"
	"seedhammer.com/stepper"
)

type engraveJob struct {
	quit       chan<- struct{}
	progresses chan stepper.Progress
	errs       chan error

	progress stepper.Progress
	done     bool
	err      error
}

func newEngraverJob(p Platform, spline bspline.Curve) *engraveJob {
	errs := make(chan error, 1)
	progress := make(chan stepper.Progress, 1)
	quit := make(chan struct{})
	e := &engraveJob{
		errs:       errs,
		progresses: progress,
		quit:       quit,
	}
	pspline := func(yield func(bspline.Knot) bool) {
		// var ticks uint
		for k := range spline {
			if !yield(k) {
				return
			}
			// ticks += k.T
			// select {
			// case <-progress:
			// default:
			// }
			// progress <- ticks
			// p.Wakeup()
		}
	}
	go func() {
		defer p.Wakeup()
		errs <- p.Engrave(true, pspline, quit, progress)
	}()
	return e
}

func (e *engraveJob) Progress() stepper.Progress {
	select {
	case p := <-e.progresses:
		e.progress = p
	default:
	}
	return e.progress
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

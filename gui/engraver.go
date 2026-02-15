package gui

import (
	"seedhammer.com/bspline"
)

type engraveJob struct {
	quit       chan<- struct{}
	progresses chan uint
	errs       chan error

	done bool
	err  error
}

func newEngraverJob(p Platform, spline bspline.Curve) *engraveJob {
	errs := make(chan error, 1)
	progress := make(chan uint, 1)
	quit := make(chan struct{})
	e := &engraveJob{
		errs:       errs,
		progresses: progress,
		quit:       quit,
	}
	go func() {
		defer p.Wakeup()
		errs <- p.Engrave(true, spline, quit, progress)
	}()
	return e
}

func (e *engraveJob) Progress() uint {
	select {
	case p := <-e.progresses:
		return p
	default:
		return 0
	}
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

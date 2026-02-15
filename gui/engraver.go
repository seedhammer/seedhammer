package gui

import (
	"seedhammer.com/bspline"
	"seedhammer.com/engrave"
	"seedhammer.com/stepper"
)

type Engraver interface {
	stepper.Writer
	Close() error
	Stats() EngraverStats
}

type engraveJob struct {
	pl     Platform
	spline bspline.Curve
	opts   jobOptions

	quit      chan<- struct{}
	errs      <-chan error
	progress  <-chan uint
	lock      chan Engraver
	status    engraveStatus
	nknots    int
	safePoint engrave.SafePointer
}

type jobOptions int

const (
	suppressStalls jobOptions = 1 << iota
)

type engraveStatus struct {
	State engraveState
	// Completed is the number of engraver ticks completed.
	Completed uint
	// Error is the error message, in case of state
	// engraveFailed.
	Error string
}

type engraveState int

const (
	engraveIdle engraveState = iota
	engraveRunning
	engraveStopping
	engraveStopped
	engraveFailed
	engraveDone
)

func newEngraverJob(p Platform, spline bspline.Curve, opts jobOptions) *engraveJob {
	return &engraveJob{
		pl:     p,
		spline: spline,
		opts:   opts,
	}
}

func (e *engraveJob) Stop() {
	if e.status.State != engraveRunning {
		return
	}
	e.status.State = engraveStopping
	if e.quit != nil {
		close(e.quit)
		e.quit = nil
	}
}

func (e *engraveJob) Start() {
	if e.errs != nil {
		// Job is already running.
		return
	}
	errs := make(chan error, 1)
	progress := make(chan uint, 1)
	quit := make(chan struct{})
	e.lock = make(chan Engraver, 1)
	e.errs = errs
	e.quit = quit
	e.progress = progress
	e.status.Error = ""
	e.status.State = engraveRunning
	go func() {
		defer e.pl.Wakeup()
		errs <- e.runEngraving(quit, progress)
	}()
}

func (e *engraveJob) Stats() EngraverStats {
	select {
	case d := <-e.lock:
		st := d.Stats()
		e.lock <- d
		return st
	default:
		return EngraverStats{}
	}
}

func (e *engraveJob) Status() engraveStatus {
	select {
	case p := <-e.progress:
		e.status.Completed += p
	default:
	}
	select {
	case err := <-e.errs:
		e.errs = nil
		if e.status.State == engraveStopping {
			e.status.State = engraveStopped
		} else {
			e.status.State = engraveDone
		}
		if err != nil {
			e.status.State = engraveFailed
			e.status.Error = err.Error()
		}
	default:
	}
	if e.status.State == engraveRunning {
		// Restart if requested.
		e.Start()
	}
	return e.status
}

func (e *engraveJob) runEngraving(quit <-chan struct{}, progress chan uint) (cerr error) {
	stall := e.opts&suppressStalls == 0
	d, err := e.pl.Engraver(stall)
	if err != nil {
		return err
	}
	e.lock <- d
	defer func() {
		d := <-e.lock
		if err := d.Close(); cerr == nil {
			cerr = err
		}
	}()

	drv := stepper.NewDriver(d)
	conf := e.pl.EngraverParams().StepperConfig
	res := newSplineResumer(drv, e.safePoint.Resume(conf))
	skipKnots := e.nknots
	for k := range e.spline {
		// TODO: use iter.Pull to resume the spline if the goroutine stack cost is
		// reasonable.
		if skipKnots > 0 {
			skipKnots--
			continue
		}
		e.nknots++
		t, err := res.Knot(k)
		e.safePoint.Knot(k)
		e.safePoint.Progress(t)
		if !reportProgress(quit, progress, t) || err != nil {
			return err
		}
	}
	return drv.Flush()
}

func reportProgress(quit <-chan struct{}, progress chan uint, t uint) bool {
	var p0 uint
	select {
	case <-quit:
		return false
	case p0 = <-progress:
		progress <- t + p0
	case progress <- t:
	}
	return true
}

type Knotter interface {
	Knot(k bspline.Knot) (completed uint, err error)
}

func newSplineResumer(drv Knotter, catchup []bspline.Knot) *splineResumer {
	return &splineResumer{
		drv:     drv,
		catchup: catchup,
	}
}

type splineResumer struct {
	drv      Knotter
	catchup  []bspline.Knot
	progress int
}

func (s *splineResumer) Knot(k bspline.Knot) (completed uint, cerr error) {
	if c := s.catchup; c != nil {
		s.catchup = nil
		// Fast forward until the most recent knot.
		for _, k := range c {
			t, err := s.drv.Knot(k)
			s.progress += int(t)
			// Don't (double-)count the resuming knots as progress on the original spline.
			s.progress -= int(k.T)
			if err != nil {
				return 0, err
			}
		}
	}
	t, err := s.drv.Knot(k)
	s.progress += int(t)
	p := max(0, s.progress)
	s.progress -= p
	return uint(p), err
}

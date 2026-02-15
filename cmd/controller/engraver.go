//go:build tinygo

package main

import (
	"errors"
	"fmt"
	"iter"
	"machine"
	"slices"
	"sync/atomic"
	"time"

	"seedhammer.com/bezier"
	"seedhammer.com/bspline"
	"seedhammer.com/driver/mjolnir2"
	"seedhammer.com/driver/tmc2209"
	"seedhammer.com/engrave"
	"seedhammer.com/gui"
	"seedhammer.com/stepper"
)

type engraver struct {
	XAxis, YAxis     *tmc2209.Device
	Dev              *mjolnir2.Device
	stepperCurrent   int
	xstalls, ystalls atomic.Uint32
	quit             chan error
	busy             chan struct{}
	// S_DIAG high means a fault on boards >= v1.5.0.
	// To detect whether S_DIAG is available, it is pulled
	// high and sdiagAvailable tracks whether its been seen
	// low.
	sdiagAvailable bool

	// Engraver state.
	powerOff func() error

	// Interrupt handler state.
	modes   chan engraveMode
	mode    engraveMode
	blocked engraveAxis
}

type engraveMode int

const (
	modeEngrave engraveMode = iota
	modeHoming
	modeNostall
)

type engraveAxis uint8

const (
	xAxis engraveAxis = 0b1 << iota
	yAxis
	sAxis
)

var (
	errPowerloss = errors.New("stepper: power loss or short circuit")
	errXBlocked  = errors.New("stepper: x-axis blocked")
	errYBlocked  = errors.New("stepper: y-axis blocked")
	errHomed     = errors.New("homing complete")
)

func configEngraverPins() {
	DRV_ENABLE.Configure(machine.PinConfig{Mode: machine.PinOutput})
	DRV_ENABLE.Set(false)
	// Configure diagnostics/fault pins to be pulled high, so that even
	// disconnected pins signal faults.
	X_DIAG.Configure(machine.PinConfig{Mode: machine.PinInputPullup})
	Y_DIAG.Configure(machine.PinConfig{Mode: machine.PinInputPullup})
	// In addition, S_DIAG used to serve as LCD_RDX and must be pulled up
	// for compatibility with boards revisions before v1.5.0.
	S_DIAG.Configure(machine.PinConfig{Mode: machine.PinInputPullup})
}

func configEngraver(voltage int) (*engraver, error) {
	uart, err := tmc2209.NewUART(stepperPIO, STEPPER_UART)
	if err != nil {
		return nil, err
	}
	X := &tmc2209.Device{
		Bus:    uart,
		Addr:   X_ADDR,
		Invert: invertX,
		Sense:  senseResistance,
	}
	Y := &tmc2209.Device{
		Bus:    uart,
		Addr:   Y_ADDR,
		Invert: invertY,
		Sense:  senseResistance,
	}

	d := &mjolnir2.Device{
		Pio:            engraverPIO,
		BasePin:        engraverBasePin,
		TicksPerSecond: engraverConf.TicksPerSecond,
	}
	if P_ADC != machine.NoPin {
		machine.InitADC()
		in := &machine.ADC{P_ADC}
		if err := in.Configure(machine.ADCConfig{}); err != nil {
			return nil, err
		}
		d.PulseADC = in
	}
	quit := make(chan error, 1)
	// bufferDur is the maximum duration the engraver can be starved without
	// stalling.
	const bufferDur = 100 * time.Millisecond
	// Perform a linear interpolation of the voltage into the range of needle
	// activation durations.
	needleAct := (needleActivationMinVoltage*time.Duration(maxVoltage-voltage) +
		needleActivationMaxVoltage*time.Duration(voltage-minVoltage)) / (maxVoltage - minVoltage)
	if err := d.Configure(bufferDur, needleAct, needlePeriod); err != nil {
		return nil, err
	}
	// Set engraver pulsed current limit through the S_VREF pin.
	// Production boards fix the limit through on-board resistors.
	if Ichop > 0 {
		pwmS_VREF.Configure(machine.PWMConfig{})
		ch, err := pwmS_VREF.Channel(S_VREF)
		if err != nil {
			return nil, err
		}
		// Compute reference voltage using formula (1) in the
		// DRV8701 datasheeti[0].
		//
		// [0]: https://www.ti.com/lit/ds/symlink/drv8701.pdf

		// Datasheet constants.
		const (
			// Voff in mV.
			Voff = 50
			// Av in V/V.
			Av = 20
		)

		// Vref in mV.
		const Vref = Ichop*Av*Rsense + Voff
		// Vmax is the voltage at 100% PWM duty cycle.
		const Vmax = 3300
		duty := pwmS_VREF.Top() * Vref / Vmax
		pwmS_VREF.Set(ch, duty)
	}
	busy := make(chan struct{}, 1)
	busy <- struct{}{}
	e := &engraver{
		Dev:            d,
		XAxis:          X,
		YAxis:          Y,
		busy:           busy,
		stepperCurrent: stepperPower * 1000 / voltage,
		quit:           quit,
		modes:          make(chan engraveMode, 1),
	}

	return e, nil
}

func (e *engraver) configureAxes() error {
	axes := []*tmc2209.Device{e.XAxis, e.YAxis}
	for i, axis := range axes {
		if err := axis.SetupSharedUART(); err != nil {
			return fmt.Errorf("axis %d: configuring UART: %w", i, err)
		}
	}
	for i, axis := range axes {
		if err := axis.Configure(); err != nil {
			return fmt.Errorf("axis %d: configure: %w", i, err)
		}
		if err := axis.SetStallThreshold(stallThreshold); err != nil {
			return fmt.Errorf("axis %d: stall threshold: %w", i, err)
		}
		if err := axis.SetMinimumStallVelocity(minimumStallVelocity); err != nil {
			return fmt.Errorf("axis %d: stepper stall velocity: %w", i, err)
		}
	}
	return nil
}

func (e *engraver) home() error {
	home := func(yield func(engrave.Command) bool) {
		home := bezier.Point{
			X: -homingDist,
			Y: -homingDist,
		}
		yield(engrave.Move(home))
	}
	conf := engraverConf
	conf.Speed = homingSpeed
	spline := engrave.PlanEngraving(conf, home)
	e.SwitchMode(modeHoming)
	if err := e.stepSpline(spline); err != errHomed {
		if err == nil {
			err = errors.New("stepper: homing timed out")
		}
		return err
	}
	// Homing disabled both axis stepper pins. Reset the driver to
	// force it to reset the pins.
	e.Dev.Reset()

	moveToOrigin := engrave.Engraving(slices.Values([]engrave.Command{
		engrave.Move(bezier.Pt(originX, originY)),
	}))
	spline = engrave.PlanEngraving(conf, moveToOrigin)
	e.SwitchMode(modeEngrave)
	if err := e.stepSpline(spline); err != nil {
		return err
	}
	return e.Dev.Flush()
}

func (e *engraver) SwitchMode(m engraveMode) {
	select {
	case <-e.modes:
	default:
	}
	e.modes <- m
	e.xstalls.Store(0)
	e.ystalls.Store(0)
}

func (e *engraver) powerOn() (close func() error, err error) {
	defers := new(defers)
	defer func() {
		if err != nil {
			defers.Call()
		}
	}()
	defers.Add(func() error {
		// Disable the power circuitry.
		DRV_ENABLE.Set(false)
		// Wait a bit for the discharge circuit to empty the capacitors.
		time.Sleep(500 * time.Millisecond)
		return nil
	})
	// Give the capacitor bank time to charge (boards >= v1.5.0).
	time.Sleep(time.Second)
	// Enable the power circuitry.
	DRV_ENABLE.Set(true)
	// Boards < v1.5.0 charge the capacitors here.
	time.Sleep(500 * time.Millisecond)
	if err := e.configureAxes(); err != nil {
		return nil, err
	}

	// Wait a bit before enabling each stepper.
	time.Sleep(200 * time.Millisecond)
	defers.Add(func() error {
		err1 := e.XAxis.Enable(0)
		err2 := e.YAxis.Enable(0)
		if err1 != nil {
			return err1
		}
		return err2
	})
	xerr := e.XAxis.Enable(e.stepperCurrent)
	if xerr != nil {
		return nil, xerr
	}
	time.Sleep(200 * time.Millisecond)
	yerr := e.YAxis.Enable(e.stepperCurrent)
	if yerr != nil {
		return nil, yerr
	}
	// Wait for standstill tuning of the drivers.
	time.Sleep(tmc2209.StandstillTuningPeriod)
	// Empty quit channel.
	for range len(e.quit) {
		<-e.quit
	}
	for _, pin := range []machine.Pin{X_DIAG, Y_DIAG, S_DIAG} {
		if err := pin.SetInterrupt(machine.PinRising, e.handleDiag); err != nil {
			return nil, fmt.Errorf("engraver: %w", err)
		}
		defers.Add(func() error { return pin.SetInterrupt(0, nil) })
	}
	e.sdiagAvailable = e.sdiagAvailable || !S_DIAG.Get()
	if e.sdiagAvailable && S_DIAG.Get() {
		return nil, errors.New("engraver: not enough power available")
	}
	return defers.Call, nil
}

func (e *engraver) EngraverStats() gui.EngraverStats {
	xload, err1 := e.XAxis.Load()
	yload, err2 := e.YAxis.Load()
	xstep, err3 := e.XAxis.StepDuration()
	ystep, err4 := e.YAxis.StepDuration()
	err := err1
	switch {
	case err2 != nil:
		err = err2
	case err3 != nil:
		err = err3
	case err4 != nil:
		err = err4
	}
	xspeed, yspeed := 0, 0
	if xstep > 0 {
		xspeed = int(time.Second / (mm * xstep))
	}
	if ystep > 0 {
		yspeed = int(time.Second / (mm * ystep))
	}
	xstalls, ystalls := int(e.xstalls.Load()), int(e.ystalls.Load())
	return gui.EngraverStats{
		StallSpeed: minimumStallVelocity / mm,
		XSpeed:     xspeed,
		YSpeed:     yspeed,
		XLoad:      xload,
		YLoad:      yload,
		XStalls:    xstalls,
		YStalls:    ystalls,
		Error:      err,
	}
}

func (e *engraver) handleDiag(pin machine.Pin) {
	// Update mode.
	select {
	case e.mode = <-e.modes:
		e.blocked = 0
	default:
	}

	stepPin, otherPin := machine.NoPin, machine.NoPin
	var a engraveAxis
	switch pin {
	case X_DIAG:
		e.xstalls.Add(1)
		stepPin = X_STEP
		otherPin = Y_STEP
		a = xAxis
	case Y_DIAG:
		e.ystalls.Add(1)
		stepPin = Y_STEP
		otherPin = X_STEP
		a = yAxis
	case S_DIAG:
		a = sAxis
	default:
		return
	}
	if e.mode == modeNostall {
		return
	}
	// Disconnect the step pin from the PIO program and set it low.
	stepPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	stepPin.Low()
	// And the needle pin, in case of an active engraving.
	NEEDLE.Configure(machine.PinConfig{Mode: machine.PinOutput})
	NEEDLE.Low()
	if e.mode != modeHoming {
		// Disable both axes in case of blockage.
		otherPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
		otherPin.Low()
	}
	e.blocked |= a
	if e.blocked&sAxis != 0 {
		e.quitEngraving(errPowerloss)
	}
	if e.mode == modeHoming {
		if e.blocked == (xAxis | yAxis) {
			e.quitEngraving(errHomed)
		}
	} else {
		switch {
		case e.blocked&xAxis != 0:
			e.quitEngraving(errXBlocked)
		case e.blocked&yAxis != 0:
			e.quitEngraving(errYBlocked)
		}
	}
}

func (e *engraver) Open() error {
	<-e.busy
	if err := e.ensurePowered(); err != nil {
		e.Close()
		return err
	}

	return nil
}

func (e *engraver) Write(steps []uint32) (int, error) {
	if len(steps) == 0 {
		return 0, nil
	}
	if err := e.ensurePowered(); err != nil {
		return 0, err
	}
	progress, err := e.Dev.Write(steps)
	if err == nil {
		select {
		case err = <-e.quit:
		default:
		}
	}
	return progress, err
}

func (e *engraver) Close() (cerr error) {
	defer func() {
		e.busy <- struct{}{}
	}()
	f := e.powerOff
	if f == nil {
		return nil
	}
	e.powerOff = nil
	return f()
}

func (e *engraver) ensurePowered() error {
	if e.powerOff != nil {
		return nil
	}
	powerOff, err := e.powerOn()
	if err != nil {
		return err
	}
	e.powerOff = powerOff
	return nil
}

func (e *engraver) quitEngraving(err error) {
	select {
	case e.quit <- err:
	default:
	}
}

func (e *engraver) stepSpline(spline iter.Seq[bspline.Knot]) error {
	drv := stepper.NewDriver(e)
	for k := range spline {
		if _, err := drv.Knot(k); err != nil {
			return err
		}
	}
	return drv.Flush()
}

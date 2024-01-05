//go:build tinygo

// Package bts7960 implements a needler driven by the [BTS7960 motor driver].
//
// [BTS7960]: https://www.handsontec.com/dataspecs/module/BTS7960%20Motor%20Driver.pdf
package bts7960

import "machine"

type Device struct {
	vcc   machine.Pin
	pwm   *machine.TIM
	left  side
	right side
}

type side struct {
	enable  machine.Pin
	pwm     machine.Pin
	channel uint8
}

func New(VCC, R_EN, L_EN, RPWM, LPWM machine.Pin, pwm *machine.TIM) (*Device, error) {
	d := &Device{
		vcc: VCC,
		pwm: pwm,
		left: side{
			enable: L_EN,
			pwm:    LPWM,
		},
		right: side{
			enable: R_EN,
			pwm:    RPWM,
		},
	}
	if err := d.configure(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Device) configure() error {
	if err := d.right.configure(d.pwm); err != nil {
		return err
	}
	if err := d.left.configure(d.pwm); err != nil {
		return err
	}
	const period = 1e9 / 25_000 // 25 kHz is the maximum PWM frequency of the BTS7960.
	d.vcc.Configure(machine.PinConfig{Mode: machine.PinOutput})
	d.vcc.Set(true)
	if err := d.pwm.Configure(machine.PWMConfig{Period: period}); err != nil {
		return err
	}
	return nil
}

func (d *Device) Enable(en bool) {
	if !en {
		d.Speed(0, 1)
	}
	d.right.enable.Set(en)
	d.left.enable.Set(en)
}

func (d *Device) Speed(nominator, denominator uint32) {
	top := d.pwm.Top()
	d.pwm.Set(d.left.channel, 0)
	d.pwm.Set(d.right.channel, top*nominator/denominator)
}

func (s *side) configure(pwm *machine.TIM) error {
	ch, err := pwm.Channel(s.pwm)
	if err != nil {
		return err
	}
	s.channel = ch
	s.enable.Configure(machine.PinConfig{Mode: machine.PinOutput})
	s.enable.Set(false)
	return nil
}

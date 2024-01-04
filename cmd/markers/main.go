// package markers is the tools for marking the top plate of the SeedHammer
// machine.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"image"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"seedhammer.com/driver/mjolnir"
)

var (
	serialDev = flag.String("device", "", "serial device")
	dryrun    = flag.Bool("n", false, "dry run")
	coords    = flag.String("coords", "0,0, 100,3, 179,3, 179,52, 179,131, 100,131, 100,52, 0,0", "coordinates to mark")
	repeat    = flag.Int("repeat", 1, "number of repetitions")
)

func main() {
	flag.Parse()

	valsStr := strings.Split(*coords, ",")
	if len(valsStr)%2 != 0 {
		fmt.Fprintf(os.Stderr, "-coords must specify an even number of values\n")
		os.Exit(1)
	}
	vals := make([]float32, len(valsStr))
	for i, v := range valsStr {
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 32)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid -coords value: %q\n", v)
			os.Exit(1)
		}
		vals[i] = float32(f)
	}
	points := make([]image.Point, len(vals)/2)
	for i := range points {
		points[i] = image.Point{
			X: int(float32(vals[i*2]) * mjolnir.Millimeter),
			Y: int(float32(vals[i*2+1]) * mjolnir.Millimeter),
		}
	}
	if err := engrave(*serialDev, points); err != nil {
		fmt.Fprintf(os.Stderr, "failed to engrave: %v\n", err)
		os.Exit(1)
	}
}

func engrave(dev string, coords []image.Point) error {
	s, err := mjolnir.Open(dev)
	if err != nil {
		return err
	}
	defer s.Close()

	prog := &mjolnir.Program{
		DryRun:     *dryrun,
		MoveSpeed:  0.9, // If commented out, use default from mjolnir/driver.go
		PrintSpeed: 0,   // If commented out, use default from mjolnir/driver.go
		End:        coords[len(coords)-1],
	}
	design := func() {
		for i := 0; i < *repeat; i++ {
			for _, c := range coords {
				szf := 2.0 * mjolnir.Millimeter
				sz := int(szf)

				left := c.Add(image.Pt(-sz, 0))
				if left.X < 0 {
					left.X = 0
				}
				prog.Move(left)
				prog.Line(c.Add(image.Pt(+sz, 0)))
				top := c.Add(image.Pt(0, -sz))
				if top.Y < 0 {
					top.Y = 0
				}
				prog.Move(top)
				prog.Line(c.Add(image.Pt(0, +sz)))
			}
		}
	}
	design()
	prog.Prepare()
	quit := make(chan os.Signal, 1)
	cancel := make(chan struct{})
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	engraveErr := make(chan error)
	go func() {
		<-quit
		signal.Reset(os.Interrupt)
		close(cancel)
		<-engraveErr
		os.Exit(1)
	}()
	go func() {
		engraveErr <- mjolnir.Engrave(s, prog, nil, cancel)
	}()
	design()
	return <-engraveErr
}

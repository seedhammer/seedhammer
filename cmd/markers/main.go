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

	"seedhammer.com/mjolnir"
)

var (
	serialDev = flag.String("device", "", "serial device")
	dryrun    = flag.Bool("n", false, "dry run")
	//coords    = flag.String("coords", "0,0, 100,3, 179,3, 179,52, 179,131, 100,131, 100,52", "coordinates to mark")
	//repeat    = flag.Int("repeat", 10, "number of repetitions")
	repeat = flag.Int("repeat", 1, "number of repetitions")
	coords = flag.String("coords", "0,0", "coordinates to mark")
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
		DryRun:    *dryrun,
		MoveSpeed: .9,
		End:       coords[len(coords)-1],
	}
	design := func() {
		for i := 0; i < *repeat; i++ {
			for _, c := range coords {
				prog.Move(c)
				prog.Line(c)
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

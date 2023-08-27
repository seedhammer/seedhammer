# SeedHammer controller program

This repository contains the source code to run the controller program for the
[SeedHammer](https://seedhammer.com) engraving machine. It runs on the same hardware
as the [SeedSigner](https://seedsigner.com/hardware): Raspberry Pi Zero or Zero W, a
WaveShare 1.3 inch 240x240 LCD hat and a Pi Zero compatible camera with a OV5647
sensor.


## Installation

Write `seedhammer-vX.Y.X.img` to an SD-card and insert that into the SD-card
slot on the Raspberry Pi.

### Linux

The `dd` command writes the image to the block device `/dev/sdX`:

```sh
$ dd if=result/seedhammer-vX.Y.Z.img of=/dev/sdX bs=1M
```

### macOS

Use a similar command as for Linux or a GUI tool such as [balenaEtcher](https://www.balena.io/etcher/).


### Building from source

To build a complete `seedhammer.img` image, [Nix](https://nixos.org/) with flakes enabled is required.
The default Nix package in `flake.nix` builds the image:

```sh
$ nix build github:seedhammer/seedhammer
$ ls result/seedhammer.img
```

The `seedhammer.img` image contains the Pi Zero firmware, the Linux kernel and drivers, and the
`controller` program that drives the Pi hardware and engraver.

To build a versioned image, use the `mkrelease` script and specify a tag:

```sh
$ nix run github:seedhammer/seedhammer#mkrelease vx.y.z
```

the resulting image will embed the version. The command also accepts git branches or commits.

### Reproducible builds

The build process is designed to be deterministic; that is, images produced with the above steps
should match the released images bit-for-bit. If not, please open an issue.

Use a tool such as `shasum` or `sha256sum` to verify that the release binary matches.


## Development

### Replacing the controller binary

To replace just the `controller` program, re-build it with Go and replace
the initial RAM filesystem. For example, if `boot` is mounted on `/Volumes/boot`:

```sh
$ CGO_ENABLED=0 GOARCH=arm GOARM=6 GOOS=linux go build ./cmd/controller
$ echo "controller" | cpio -H newc -o --quiet | gzip > /Volumes/boot/initramfs.cpio.gz
```

## Update through USB

There is a crude facility to replace and restart the controller binary on a running device. First,
build and prepare a debug build of the image

```
$ nix build .#image-debug
```

from a local clone of this repository.  Then write `result/seedhammer-debug.img` to an SD-card.
Connect the device to your machine with a USB cable to the USB port closest to the mini-HDMI
port of the device; that is, the port usually used to communicate with the engraver.

Then, to upload and run a new version of the controller binary, run

```
$ export USBDEV=/dev/cu.usbmodem101 # Or (usually) /dev/ttyUSB0 on Linux.
$ nix run .#reload $USBDEV
```

In debug mode, logging output from the controller is routed through the USB serial device.
Use

```
$ cat $USBDEV
```

to show the log on your terminal. The `nix .#reload` command automatically does this after reloading.

### Remote control

There are few commands available to remote control, or script, the device in debug mode.

```
$ echo "input up" > $USBDEV
```

sends one or more button events to the device. Available buttons are: `up`, `down`, `left`, `right`, `center`,
`b1`, `b2`, `b3`.

```
$ echo "runes ACCIDENT" > $USBDEV
```

sends text to the device, where every space and the newline sends an implicit `input b2`. Useful for scripting the input of seeds.

```
$ echo "screenshot" > $USBDEV
```

instructs the controller to dump a screenshot to the SD card.

## Dry-run engraving

Testing the engraving process without actually spending a plate can be done in dry-run mode. It's activated
by long-pressing the middle button on the engraving screen. When dry-run is enabled, a small notice is shown
in the lower right corner of the screen.

### License

The files is this repository are in the public domain as described in the [LICENSE](LICENSE) file,
except files in directories with their own LICENSE files.

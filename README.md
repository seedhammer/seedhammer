# SeedHammer II Firmware

This repository contains the source code to run the controller program for the
[SeedHammer II](https://seedhammer.com) engraving machine. The hardware is
[open source](https://github.com/seedhammer/hardware).

## Installation

Press and hold the firmware upgrade button while connecting the machine to
a computer. Then, copy the firmware file to the USB drive that appears. The
installation is complete when the drive disappears.

### Building from source

To build a [UF2](https://github.com/microsoft/uf2) image, [Nix](https://nixos.org/) with flakes
enabled is required.

```sh
$ nix run .#build-firmware
```

### Reproducible builds

The build process is designed to be deterministic, that is, images produced with the above steps
should match the released images bit-for-bit, except for the signature. To copy the signature
from an official release to a locally built firmware:

```sh
$ nix run .#copy-signature <path/to/official/seedhammerii-vX.Y.Z.uf2> seedhammerii-vX.Y.Z>
```

## Development

Connect a debugger to the debug and UART ports on the machine PCB. Then, build and flash a
firmware image:

```
$ nix run .#flash-firmware flash -tags debug
```

In debug mode, logging output from the controller is routed through the USB serial device.
Use

```
$ tinygo monitor
```

to show the log on your terminal.

### License

The files is this repository are in the public domain as described in the [LICENSE](LICENSE) file,
except files in directories with their own LICENSE files.

### Contributions

Contributors must agree to the [developer certificate of origin](https://developercertificate.org/),
to ensure their work is compatible with the the LICENSE. Sign your commits with
Signed-off-by statements to show your agreement with the `git commit --signoff` (or `-s`)
command.

# SeedHammer II Firmware

This repository contains the source code to run the controller program for the
[SeedHammer II](https://seedhammer.com) engraving machine. The hardware is
[open source](https://github.com/seedhammer/hardware).

## Installation

Firmware installation requires an Android or Windows device.

Connect the USB-C cable to the Android or Windows device first, but do not
connect it to the machine yet. Press and hold the white firmware upgrade button
on the underside of the control board above the hammerhead, then connect the
cable to the machine while continuing to hold the button. Release the button
when a USB drive appears on the Android or Windows device. The machine screen
may remain blank or show only the backlight while it is in firmware upgrade
mode.

Copy the firmware file to the USB drive. The machine restarts when the
installation is complete and shows the installed firmware version on screen.

### Building from source

To build a [UF2](https://github.com/microsoft/uf2) image, [Nix](https://nixos.org/) with flakes
enabled is required.

```sh
$ nix run .#build-firmware
```

### Reproducible builds

The build process is designed to be deterministic: a local build from the same
source and version should match the released image, except for the signature.
To reproduce an official release, set the release version once and reuse it for
the verification commands. The examples below use Nix to provide tools such as
`curl` and `sha256sum`.

```sh
$ export VERSION=v1.4.1
```

Check out the release tag:

```sh
$ git switch --detach "$VERSION"
```

Build the tagged source with the same version string used by the release
workflow:

```sh
$ nix run .#build-firmware
```

Download the official release image:

```sh
$ nix shell nixpkgs#curl -c curl -L \
    -o "official-seedhammerii-$VERSION.uf2" \
    "https://github.com/seedhammer/seedhammer/releases/download/$VERSION/seedhammerii-$VERSION.uf2"
```

Local builds do not have the SeedHammer release signing key, so the complete
UF2 file will not match the official release until the official signature is
copied into the local file. Before copying the signature, compare the signed
firmware payload with the `picosign` tool included in this repository. The Nix
development shell provides Go, so no separate Go installation is required:

```sh
$ nix develop -c go run seedhammer.com/cmd/picosign hash "seedhammerii-$VERSION.uf2" | nix shell nixpkgs#coreutils -c sha256sum
$ nix develop -c go run seedhammer.com/cmd/picosign hash "official-seedhammerii-$VERSION.uf2" | nix shell nixpkgs#coreutils -c sha256sum
```

To copy the signature from an official release to a locally built firmware:

```sh
$ nix run .#copy-signature "official-seedhammerii-$VERSION.uf2" "seedhammerii-$VERSION.uf2"
```

After copying the signature, the full files should have identical SHA-256 sums:

```sh
$ nix shell nixpkgs#coreutils -c sha256sum "seedhammerii-$VERSION.uf2" "official-seedhammerii-$VERSION.uf2"
```

After verifying that the SHA-256 sums are identical, return to the main branch:

```sh
$ git switch main
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

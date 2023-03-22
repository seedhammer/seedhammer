{
  description = "Builds Seedhammer disk image for Raspberry Pi";

  inputs = {
    nixpkgs.url = "nixpkgs";
    utils.url = "github:numtide/flake-utils";
    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.utils.follows = "utils";
    };
  };

  outputs = { self, nixpkgs, utils, gomod2nix }:
    utils.lib.eachDefaultSystem (system:
      let
        arch = builtins.head (builtins.split "-" system);
        darwinpkgs = import nixpkgs {
          system = "${arch}-darwin";
        };
        localpkgs = import nixpkgs {
          inherit system;
        };
        linuxpkgs = import nixpkgs {
          system = "${arch}-linux";
        };
        crosspkgsFor = system: import nixpkgs {
          inherit system;
          crossSystem = {
            system = "armv6l-linux";
            gcc = {
              arch = "armv6k";
              fpu = "vfp";
            };
          };
          overlays = [
            gomod2nix.overlays.default
            (self: super: {
              go = super.go_1_20.overrideAttrs (finalAttrs: previousAttrs: {
                patches = [
                  # Original patches, except those that result in Go binaries
                  # with /nix/store references.
                  ./patches/remove-tools-1.11.patch
                  ./patches/go_no_vendor_checks-1.16.patch
                ];
              });
            })
          ];
        };
        crosspkgs = crosspkgsFor "${arch}-linux";
        timestamp = "2009/01/03T12:15:05";
        loader-lib = "ld-musl-armhf.so.1";
      in
      {
        formatter = localpkgs.nixpkgs-fmt;
        apps = {
          gomod2nix = {
            type = "app";
            program = "${gomod2nix.packages.${system}.default}/bin/gomod2nix";
          };
        };
        lib = {
          mkkernel =
            let
              pkgs = linuxpkgs;
            in
            debug: pkgs.stdenvNoCC.mkDerivation {
              name = "Raspberry Pi Linux kernel";

              src = pkgs.fetchFromGitHub {
                owner = "raspberrypi";
                repo = "linux";
                rev = "45d339389bb85588b8045dd40a00c54d01e2e711";
                sha256 = "Z/0TVDYRPDucCiP1aqc6B2vJLxdg1ecrUhrtPI2037I=";
              };

              # For reproducible builds.
              KBUILD_BUILD_TIMESTAMP = timestamp;
              KBUILD_BUILD_USER = "seedhammer";
              KBUILD_BUILD_HOST = "seedhammer.com";

              enableParallelBuilding = true;

              makeFlags = [
                "ARCH=arm"
                "CROSS_COMPILE=armv6k-unknown-linux-gnueabihf-"
                "LLVM=1"
              ];

              nativeBuildInputs = with pkgs.buildPackages; [
                clang_14
                llvm_14
                lld_14
                bison
                flex
                openssl
                bc
                ncurses
                perl
              ];

              postPatch = ''
                patchShebangs scripts/config
              '';

              configurePhase = ''
                make $makeFlags -j$NIX_BUILD_CORES bcmrpi_defconfig

                # Disable networking (including bluetooth).
                ./scripts/config --disable NET
                # Disable sound support.
                ./scripts/config --disable SOUND
                # Disable features we don't need.
                ./scripts/config --disable EXT4_FS
                ./scripts/config --disable F2FS_FS
                ./scripts/config --disable PSTORE
                ./scripts/config --disable NLS_CODEPAGE_437
                ./scripts/config --disable INPUT_TOUCHSCREEN
                ./scripts/config --disable RC_MAP
                # Enable v4l2
                ./scripts/config --enable MEDIA_SUPPORT
                ./scripts/config --enable VIDEO_V4L2
                ./scripts/config --enable VIDEO_DEV
                # Enable camera driver.
                ./scripts/config --enable I2C_BCM2835
                ./scripts/config --enable I2C_MUX
                ./scripts/config --enable REGULATOR_FIXED_VOLTAGE
                ./scripts/config --enable I2C_MUX_PINCTRL
                ./scripts/config --enable VIDEO_BCM2835_UNICAM
                ./scripts/config --enable VIDEO_CODEC_BCM2835
                ./scripts/config --enable VIDEO_ISP_BCM2835
                ./scripts/config --enable VIDEO_OV5647
                # Enable SPI.
                ./scripts/config --enable SPI_BCM2835
                ./scripts/config --enable SPI_SPIDEV
                # Enable FTDI USB serial driver.
                ./scripts/config --enable USB_SERIAL
                ./scripts/config --enable USB_SERIAL_CONSOLE
                ./scripts/config --enable USB_SERIAL_FTDI_SIO
                # Disable BCM2835_FAST_MEMCPY which fails to compile with clang.
                ./scripts/config --disable BCM2835_FAST_MEMCPY
              '' + (if debug then ''
                ./scripts/config --disable CONFIG_USB_DWCOTG
                ./scripts/config --enable CONFIG_USB_DWC2
                ./scripts/config --enable CONFIG_USB_G_SERIAL
                # For mounting the SD card.
                ./scripts/config --enable NLS_CODEPAGE_437
              '' else "");

              buildPhase = ''
                make $makeFlags -j$NIX_BUILD_CORES zImage dtbs
              '';

              installPhase = ''
                mkdir -p $out/overlays
                cp arch/arm/boot/zImage $out/kernel.img
                cp arch/arm/boot/dts/*rpi-zero*.dtb $out/
                cp arch/arm/boot/dts/overlays/dwc2.dtbo $out/overlays/
                cp arch/arm/boot/dts/overlays/ov5647.dtbo $out/overlays/
              '';

              allowedReferences = [ ];
            };
          mkimage = debug:
            let
              pkgs = linuxpkgs;
              firmware = self.packages.${system}.firmware;
              kernel =
                if debug then
                  self.packages.${system}.kernel-debug
                else
                  self.packages.${system}.kernel;

              controller =
                if debug then
                  self.packages.${system}.controller-debug
                else
                  self.packages.${system}.controller;
              img-name = if debug then "seedhammer-debug.img" else "seedhammer.img";
              cmdlinetxt = pkgs.writeText "cmdline.txt" "console=tty1 rdinit=/controller oops=panic devfs=nomount";
              configtxt = pkgs.writeText "config.txt" (''
                initramfs initramfs.cpio.gz followkernel
                disable_splash=1
                dtparam=spi=on
                boot_delay=0
                camera_auto_detect=1
              '' + (if debug then "dtoverlay=dwc2" else ""));
            in
            pkgs.stdenvNoCC.mkDerivation {
              name = "build image template";

              dontUnpack = true;
              buildPhase = ''
                sectorsToBlocks() {
                  echo $(( ( "$1" * 512 ) / 1024 ))
                }

                sectorsToBytes() {
                  echo $(( "$1" * 512 ))
                }

                # Create disk image.
                dd if=/dev/zero of=disk.img bs=1M count=15
                ${pkgs.util-linux}/bin/sfdisk disk.img <<EOF
                  label: dos
                  label-id: 0xceedb0at

                  disk.img1 : type=c, bootable
                EOF

                # Create boot partition.
                eval $(${pkgs.util-linux}/bin/partx disk.img -o START,SECTORS --nr 1 --pairs)
                ${pkgs.dosfstools}/bin/mkfs.vfat --invariant -i deadbeef -n boot disk.img --offset $START $(sectorsToBlocks $SECTORS)
                OFFSET=$(sectorsToBytes $START)

                # Copy boot files.
                mkdir -p boot/overlays overlays
                cp ${cmdlinetxt} boot/cmdline.txt
                cp ${configtxt} boot/config.txt
                cp ${firmware}/boot/bootcode.bin ${firmware}/boot/start.elf \
                  ${firmware}/boot/fixup.dat \
                  ${kernel}/kernel.img ${kernel}/*.dtb boot/
                cp ${kernel}/overlays/* overlays/

                # Create initramfs with controller program and libraries.
                mkdir initramfs
                cp ${controller}/bin/controller initramfs/controller
                cp -R "${self.packages.${system}.camera-driver}"/* initramfs/
                # Set constant mtimes and permissions for determinism.
                chmod 0755 `find initramfs`
                ${pkgs.coreutils}/bin/touch -d '${timestamp}' `find initramfs`

                ${pkgs.findutils}/bin/find initramfs -mindepth 1 -printf '%P\n'\
                  | sort \
                  | ${pkgs.cpio}/bin/cpio -D initramfs --reproducible -H newc -o --owner 0:0 --quiet \
                  | ${pkgs.gzip}/bin/gzip > boot/initramfs.cpio.gz

                chmod 0755 `find boot overlays`
                ${pkgs.coreutils}/bin/touch -d '${timestamp}' `find boot overlays`
                ${pkgs.mtools}/bin/mcopy -bpm -i "disk.img@@$OFFSET" boot/* ::
                # mcopy doesn't copy directories deterministically, so rely on sorted shell globbing
                # instead.
                ${pkgs.mtools}/bin/mcopy -bpm -i "disk.img@@$OFFSET" overlays/* ::overlays

                # Output offset of partition for stamping version in release.
                echo -n $(( $START*512)) > partition_offset_bytes
              '';

              installPhase = ''
                mkdir -p $out
                cp partition_offset_bytes $out/
                cp disk.img $out/${img-name}
              '';

              allowedReferences = [ ];
            };
          mkcontroller = debug:
            let pkgs = crosspkgs.pkgsMusl; in pkgs.buildGoApplication {
              name = "controller";
              src = ./.;
              modules = ./gomod2nix.toml;
              tags = [ "netgo" ] ++ (if debug then [ "debug" ] else [ ]);
              subPackages = [ "cmd/controller" ];
              # PIE is required by musl.
              buildFlags = "-buildmode=pie";
              CGO_ENABLED = 1;
              GOARM = "6";
              # Strip is not deterministic.
              dontStrip = true;
              # Don't include debug information.
              ldflags = "-w";

              fixupPhase = ''
                patchelf --set-rpath "/lib" \
                  --set-interpreter "/lib/${loader-lib}" \
                  --add-needed "/lib/v4l2-compat.so" \
                  $out/bin/controller
              '';

              allowedReferences = [ ];
            };
        };
        packages =
          {
            controller = self.lib.${system}.mkcontroller false;
            controller-debug = self.lib.${system}.mkcontroller true;
            libcamera =
              let
                pkgs = crosspkgs.pkgsMusl;
                cross-file = pkgs.writeText "cross-file.conf" ''
                  [properties]
                  needs_exe_wrapper = true

                  [host_machine]
                  system = 'linux'
                  cpu_family = 'arm'
                  cpu = 'armv6l'
                  endian = 'little'

                  [binaries]
                  llvm-config = 'llvm-config-native'
                '';
              in
              pkgs.stdenv.mkDerivation {
                name = "libcamera";

                src = pkgs.fetchgit {
                  url = "https://git.libcamera.org/libcamera/libcamera.git";
                  rev = "v0.0.4";
                  hash = "sha256-MFf/qH7QBZfZ0v3AmYEGMKEHnB0/Qf6WYFYvHAIV6Cw=";
                };

                patches = [
                  # Disable IPA signature validation: we don't need it and it depends
                  # on openssl which is fiddly to build reproducibly.
                  ./patches/libcamera_disable_signature.patch

                  # Open V4L2 devices with O_CLOEXEC so that our debug builds
                  # can reload themselves while using the camera.
                  ./patches/libcamera_cloexec.patch

                  # Minimize number of buffers to reduce latency.
                  ./patches/libcamera_min_buffers.patch
                ];

                postPatch = ''
                  patchShebangs utils/
                '';

                dontFixup = true;
                dontStrip = true;
                dontUseNinjaInstall = true;
                dontUseMesonConfigure = true;

                nativeBuildInputs = with pkgs.pkgsBuildHost; [
                  meson
                  ninja
                  pkg-config
                  python3
                  python3Packages.jinja2
                  python3Packages.pyyaml
                  python3Packages.ply
                ];

                buildInputs = with pkgs.pkgsStatic; [
                  libyaml
                ];

                configurePhase = ''
                  meson setup build \
                    -Dv4l2=true \
                    -Dqcam=disabled \
                    -Dcam=disabled \
                    -Dgstreamer=disabled \
                    -Dtracing=disabled \
                    -Dpipelines=raspberrypi \
                    -Ddocumentation=disabled \
                    -Dlc-compliance=disabled \
                    -Dwerror=false \
                    --cross-file=${cross-file} \
                    --buildtype=plain \
                    --prefix=/ \
                    --libdir=lib
                  cd build
                  export
                  echo "LDFLAGS" $NIX_LDFLAGS
                '';

                ninjaFlags = [ "-v" ];

                installPhase = ''
                  mkdir -p $out/lib/libcamera $out/share/libcamera/ipa/raspberrypi
                  cp ./src/libcamera/base/libcamera-base.so.0.0.4 \
                    ./src/libcamera/libcamera.so.0.0.4 \
                    ./src/v4l2/v4l2-compat.so \
                    $out/lib
                  cp ./src/ipa/raspberrypi/ipa_rpi.so $out/lib/libcamera
                  export
                  echo $CC
                  echo $CXX
                  echo "LDFLAGS" $NIX_LDFLAGS
                  echo $PATH
                  echo `find $out/lib -type f|sort`
                  patchelf --print-rpath `find $out/lib -type f|sort`
                  cp ../src/ipa/raspberrypi/data/*.json $out/share/libcamera/ipa/raspberrypi
                  patchelf --set-rpath "/lib" `find $out/lib -type f`
                '';

                strictDeps = true;
                enableParallelBuilding = true;

                allowedReferences = [ ];
              };
            camera-driver = let pkgs = crosspkgs.pkgsMusl; in pkgs.stdenv.mkDerivation {
              name = "camera-driver";

              dontUnpack = true;
              dontStrip = true;
              dontFixup = true;

              installPhase = ''
                mkdir -p $out/lib/libcamera
                LIBCAM="${self.packages.${system}.libcamera}"
                chmod +w `find $out/`
                cclib="${pkgs.stdenv.cc.cc.lib}/${pkgs.stdenv.targetPlatform.config}/lib"
                echo "CCLIB:" $cclib

                cp "$cclib/libstdc++.so.6" \
                  "$cclib/libgcc_s.so.1" \
                  "$cclib/libatomic.so.1" \
                  $out/lib/
                chmod +w `find $out/`
                patchelf --set-rpath "/lib" `find $out/lib -type f`
                echo "******" ${pkgs.stdenv.cc.libc}
                cp "${pkgs.stdenv.cc.libc}/lib/${loader-lib}" \
                  $out/lib
                cp -R "$LIBCAM/lib/" $out/
                cp -R "$LIBCAM/share/" $out/
              '';

              strictDeps = true;

              allowedReferences = [ ];
            };
            kernel = self.lib.${system}.mkkernel false;
            kernel-debug = self.lib.${system}.mkkernel true;
            firmware = localpkgs.fetchFromGitHub {
              owner = "raspberrypi";
              repo = "firmware";
              rev = "0d6514f32722e4acf091ab2af9715793ffd6b727";
              sparseCheckout = [ "boot" ];
              sha256 = "sha256-lX33EMZas1cdCFb/UZc6yjIWZ/4Rj2/yI07GZjnL7fs=";
            };
            image = self.lib.${system}.mkimage false;
            image-debug = self.lib.${system}.mkimage true;
            reload = let pkgs = localpkgs; in pkgs.writeShellScriptBin "reload" ''
              set -e
              USBDEV=$1
              if [ -z "$1" ]; then
                  echo "error: specify USB device"
                  exit 1
              fi

              PROG="${self.packages.${system}.controller-debug}/bin/controller"

              echo "reload $(wc -c < "$PROG")" > "$USBDEV"
              cat "$PROG" > "$USBDEV"
              exec cat "$USBDEV"
            '';
            reload-fast = let pkgs = localpkgs; in pkgs.writeShellScriptBin "reload" ''
              set -e
              USBDEV=$1
              if [ -z "$USBDEV" ]; then
                  echo "error: specify USB device"
                  exit 1
              fi
              TMPDIR="$(mktemp -d)"
              trap 'rm -rf -- "$TMPDIR"' EXIT

              PROG="$TMPDIR/controller"

              CGO_ENABLED=1 \
              GOOS=linux \
              GOARCH=arm \
              GOARM=6 \
              go build -buildmode pie -ldflags=-w -trimpath -tags debug,netgo -o "$PROG" ./cmd/controller
              patchelf --set-rpath "/lib" --set-interpreter "/lib/${loader-lib}" "$PROG"
              patchelf --add-needed "/lib/v4l2-compat.so" "$PROG"

              echo "reload $(wc -c < "$PROG")" > "$USBDEV"
              cat "$PROG" > "$USBDEV"
              exec cat "$USBDEV"
            '';
            mkrelease = let pkgs = localpkgs; in pkgs.writeShellScriptBin "mkrelease" ''
              set -eu

              VERSION=$1
              if [ -z "$VERSION" ]; then
                  echo "error: specify version"
                  exit 1
              fi

              flake="github:seedhammer/seedhammer/$VERSION"
              nix build "$flake"
              nix run "$flake"#stamp-release $VERSION

              if [ -n "$SSH_SIGNING_KEY" ]; then
                ssh-keygen -Y sign -f "$SSH_SIGNING_KEY" -n seedhammer.img seedhammer-"$VERSION".img
              fi
            '';
            stamp-release = let pkgs = localpkgs; in pkgs.writeShellScriptBin "stamp-release" ''
              set -eu

              # For determinism.
              export TZ=UTC
              VERSION=$1
              if [ -z "$VERSION" ]; then
                  echo "error: specify version"
                  exit 1
              fi
              TMPDIR="$(mktemp -d)"
              trap 'rm -rf -- "$TMPDIR"' EXIT

              src="result/seedhammer.img"
              dst="seedhammer-$VERSION.img"

              # Append the version string to the kernel cmdline, to be read by the controller binary.
              # the image packages stores the partition offset for us.
              OFFSET=$(cat result/partition_offset_bytes)
              ${pkgs.mtools}/bin/mcopy -bpm -i "$src@@$OFFSET" ::cmdline.txt "$TMPDIR/"
              echo -n " sh_version=$VERSION" >> "$TMPDIR/cmdline.txt"
              # preserve attributes for determinism.
              chmod 0755 "$TMPDIR/cmdline.txt"
              ${pkgs.coreutils}/bin/touch -d '${timestamp}' "$TMPDIR/cmdline.txt"
              cp "$src" "$dst"
              chmod +w "$dst"
              ${pkgs.mtools}/bin/mdel -i "$dst@@$OFFSET" ::cmdline.txt
              ${pkgs.mtools}/bin/mcopy -bpm -i "$dst@@$OFFSET" "$TMPDIR/cmdline.txt" ::
            '';
            darwin-builder = darwinpkgs.darwin.builder;
            default = self.packages.${system}.image;
          };
        # Build a shell capable of running #reload-fast.
        devShells.default = (crosspkgsFor system).pkgsMusl.go;
      });
}

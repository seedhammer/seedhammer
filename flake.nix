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
        localpkgs = import nixpkgs {
          inherit system;
        };
        crosspkgs = import nixpkgs {
          inherit system;
          crossSystem = {
            config = "armv6l-unknown-linux-musleabihf";
            gcc = {
              arch = "armv6k";
              fpu = "vfp";
            };
          };
          overlays = [
            gomod2nix.overlays.default
          ];
        };
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
              pkgs = crosspkgs;
            in
            debug: pkgs.stdenv.mkDerivation {
              name = "Raspberry Pi Linux kernel";

              src = pkgs.fetchFromGitHub {
                owner = "raspberrypi";
                repo = "linux";
                rev = "0afb5e98488aed7017b9bf321b575d0177feb7ed";
                sha256 = "t+xq0HmT163TaE+5/sb2ZkNWDbBoiwbXk3oi6YEYsIA=";
                # Remove files that introduce case sensitivity clashes on darwin.
                postFetch = ''
                  rm $out/include/uapi/linux/netfilter/xt_*.h
                  rm $out/include/uapi/linux/netfilter_ipv4/ipt_*.h
                  rm $out/include/uapi/linux/netfilter_ipv6/ip6t_*.h
                  rm $out/net/netfilter/xt_*.c
                  rm $out/tools/memory-model/litmus-tests/Z6.0+poonce*
                '';
              };

              # For reproducible builds.
              KBUILD_BUILD_TIMESTAMP = timestamp;
              KBUILD_BUILD_USER = "seedhammer";
              KBUILD_BUILD_HOST = "seedhammer.com";

              enableParallelBuilding = true;

              makeFlags = [
                "ARCH=arm"
                "CROSS_COMPILE=${pkgs.stdenv.cc.targetPrefix}"
              ];

              depsBuildBuild = [ pkgs.buildPackages.stdenv.cc ];

              nativeBuildInputs = with pkgs.buildPackages; [
                elf-header
                bison
                flex
                openssl
                bc
                perl
              ];

              patches = [
                ./patches/kernel_missing_includes.patch
              ];

              hardeningDisable = [ "bindnow" "format" "fortify" "stackprotector" "pic" "pie" ];

              postPatch = ''
                patchShebangs scripts/config
              '';

              configurePhase = ''
                export HOSTCC=$CC_FOR_BUILD
                export HOSTCXX=$CXX_FOR_BUILD
                export HOSTAR=$AR_FOR_BUILD
                export HOSTLD=$LD_FOR_BUILD

                make $makeFlags -j$NIX_BUILD_CORES \
                  HOSTCC=$HOSTCC HOSTCXX=$HOSTCXX HOSTAR=$HOSTAR HOSTLD=$HOSTLD \
                  CC=$CC OBJCOPY=$OBJCOPY OBJDUMP=$OBJDUMP READELF=$READELF \
                  HOSTCFLAGS="-D_POSIX_C_SOURCE=200809L" \
                  bcmrpi_defconfig

                # Disable networking (including bluetooth).
                ./scripts/config --disable NET
                ./scripts/config --disable INET
                ./scripts/config --disable NETFILTER
                ./scripts/config --disable PROC_SYSCTL
                ./scripts/config --disable FSCACHE
                # There's no need for security models, and leaving it enabled
                # leads to build errors because of the files removed in postFetch above.
                ./scripts/config --disable SECURITY
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
              '' + (if debug then ''
                ./scripts/config --disable CONFIG_USB_DWCOTG
                ./scripts/config --enable CONFIG_USB_DWC2
                ./scripts/config --enable CONFIG_USB_G_SERIAL
                # For mounting the SD card.
                ./scripts/config --enable NLS_CODEPAGE_437
              '' else "");

              buildPhase = ''
                export HOSTCC=$CC_FOR_BUILD
                export HOSTCXX=$CXX_FOR_BUILD
                export HOSTAR=$AR_FOR_BUILD
                export HOSTLD=$LD_FOR_BUILD

                make $makeFlags -j$NIX_BUILD_CORES \
                  HOSTCC=$HOSTCC HOSTCXX=$HOSTCXX HOSTAR=$HOSTAR HOSTLD=$HOSTLD \
                  CC=$CC OBJCOPY=$OBJCOPY OBJDUMP=$OBJDUMP READELF=$READELF \
                  HOSTCFLAGS="-D_POSIX_C_SOURCE=200809L" \
                  zImage dtbs
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
          mkinitramfs = debug:
            let
              pkgs = localpkgs;
              controller =
                if debug then
                  self.packages.${system}.controller-debug
                else
                  self.packages.${system}.controller;
            in
            pkgs.stdenvNoCC.mkDerivation {
              name = "initramfs";

              dontUnpack = true;
              buildPhase = ''
                # Create initramfs with controller program and libraries.
                mkdir initramfs
                cp ${controller}/bin/controller initramfs/controller
                cp -R "${self.packages.${system}.camera-driver}"/* initramfs/
                # Set constant mtimes and permissions for determinism.
                chmod 0755 `find initramfs`
                ${pkgs.coreutils}/bin/touch -d '${timestamp}' `find initramfs`

                ${pkgs.findutils}/bin/find initramfs -mindepth 1 -printf '%P\n'\
                  | sort \
                  | ${pkgs.cpio}/bin/cpio -D initramfs --reproducible -H newc -o --owner +0:+0 --quiet \
                  | ${pkgs.gzip}/bin/gzip > initramfs.cpio.gz
              '';

              installPhase = ''
                mkdir -p $out
                cp initramfs.cpio.gz $out/
              '';

              allowedReferences = [ ];
            };
          mkimage = debug:
            let
              pkgs = localpkgs;
              firmware = self.packages.${system}.firmware;
              kernel =
                if debug then
                  self.packages.${system}.kernel-debug
                else
                  self.packages.${system}.kernel;

              initramfs = self.lib.${system}.mkinitramfs debug;
              img-name = if debug then "seedhammer-debug.img" else "seedhammer.img";
              cmdlinetxt = pkgs.writeText "cmdline.txt" "console=tty1 rdinit=/controller oops=panic devfs=nomount";
              configtxt = pkgs.writeText "config.txt" (''
                initramfs initramfs.cpio.gz followkernel
                disable_splash=1
                dtparam=spi=on
                boot_delay=0
                camera_auto_detect=1
              '' + (if debug then "dtoverlay=dwc2" else ""));
              util-linux = self.packages.${system}.util-linux;
            in
            pkgs.stdenvNoCC.mkDerivation {
              name = "disk-image";

              dontUnpack = true;
              buildPhase = ''
                sectorsToBlocks() {
                  echo $(( ( "$1" * 512 ) / 1024 ))
                }

                sectorsToBytes() {
                  echo $(( "$1" * 512 ))
                }

                # Create disk image.
                dd if=/dev/zero of=disk.img bs=1M count=14
                ${util-linux}/bin/sfdisk disk.img <<EOF
                  label: dos
                  label-id: 0xceedb0ad

                  disk.img1 : type=c, bootable
                EOF

                # Create boot partition.
                START=$(${util-linux}/bin/fdisk -l -o Start disk.img|tail -n 1)
                SECTORS=$(${util-linux}/bin/fdisk -l -o Sectors disk.img|tail -n 1)
                ${pkgs.dosfstools}/bin/mkfs.vfat --invariant -i deadbeef -n boot disk.img --offset $START $(sectorsToBlocks $SECTORS)
                OFFSET=$(sectorsToBytes $START)

                # Copy boot files.
                mkdir -p boot/overlays overlays
                cp ${cmdlinetxt} boot/cmdline.txt
                cp ${configtxt} boot/config.txt
                cp ${firmware}/boot/bootcode.bin ${firmware}/boot/start.elf \
                  ${firmware}/boot/fixup.dat \
                  ${kernel}/kernel.img ${kernel}/*.dtb \
                  ${initramfs}/initramfs.cpio.gz boot/
                cp ${kernel}/overlays/* overlays/

                chmod 0755 `find boot overlays`
                ${pkgs.coreutils}/bin/touch -d '${timestamp}' `find boot overlays`
                ${pkgs.mtools}/bin/mcopy -bpm -i "disk.img@@$OFFSET" boot/* ::
                # mcopy doesn't copy directories deterministically, so rely on sorted shell globbing
                # instead.
                ${pkgs.mtools}/bin/mcopy -bpm -i "disk.img@@$OFFSET" overlays/* ::overlays
              '';

              installPhase = ''
                mkdir -p $out
                cp disk.img $out/${img-name}
              '';

              allowedReferences = [ ];
            };
          mkcontroller = debug:
            let
              libcamera = self.packages.${system}.libcamera;
              pkgs = crosspkgs;
            in
            pkgs.buildGoApplication {
              go = pkgs.buildPackages.go_1_20;
              name = "controller";
              src = ./.;
              modules = ./gomod2nix.toml;
              tags = [ "netgo" ] ++ (if debug then [ "debug" ] else [ ]);
              subPackages = [ "cmd/controller" ];
              # PIE is required by musl.
              buildFlags = "-buildmode=pie";
              CGO_ENABLED = 1;
              GOARM = "6";
              CGO_CXXFLAGS = "-I${libcamera}/include";
              CGO_LDFLAGS = "-L${libcamera}/lib";
              # Go programs may break by using strip; use ldflags -w -s instead.
              dontStrip = true;
              # Don't include debug information.
              ldflags = "-w -s";

              nativeBuildInputs = with pkgs.buildPackages; [
                nukeReferences
              ];

              fixupPhase = ''
                patchelf --set-rpath "/lib" \
                  --set-interpreter "/lib/${loader-lib}" \
                  $out/bin/controller
                nuke-refs $out/bin/controller
                prefix=${pkgs.stdenv.targetPlatform.config}
                # Strip the indeterministic Go build id.
                "$prefix"-objcopy -R .note.go.buildid $out/bin/controller
              '';

              allowedReferences = [ ];
            };
        };
        packages =
          {
            controller = self.lib.${system}.mkcontroller false;
            controller-debug = self.lib.${system}.mkcontroller true;
            util-linux =
              let
                pkgs = localpkgs;
                stdenv = pkgs.stdenv;
              in
              stdenv.mkDerivation {
                name = "util-linux";

                src = pkgs.fetchFromGitHub {
                  owner = "util-linux";
                  repo = "util-linux";
                  rev = "v2.39";
                  sha256 = "udzFsLVSsNsoGkMFvJQRoD4na4U+qoSSaenoXZ4gql4=";
                };

                nativeBuildInputs = with pkgs.buildPackages; [
                  autoconf
                  automake
                  gettext
                  bison
                  libtool
                  pkg-config
                ];

                buildInputs = with pkgs; [
                  ncurses
                ];

                postPatch = pkgs.lib.optionalString stdenv.isDarwin ''
                  substituteInPlace autogen.sh --replace glibtoolize libtoolize
                '';

                configureFlags = [
                  "--disable-asciidoc"
                  "--disable-wall"
                  "--disable-mount"
                ];

                preConfigure = ''
                  ./autogen.sh
                '';
              };
            libcamera =
              let
                pkgs = crosspkgs;
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
                ];

                postPatch = ''
                  patchShebangs utils/
                '';

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
                    -Dv4l2=false \
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
                '';

                installPhase = ''
                  mkdir -p $out/lib/libcamera $out/share/libcamera/ipa/raspberrypi $out/include
                  cp -P ./src/libcamera/base/libcamera-base.so.0.0.4 \
                    ./src/libcamera/base/libcamera-base.so \
                    ./src/libcamera/libcamera.so.0.0.4 \
                    ./src/libcamera/libcamera.so \
                    $out/lib
                  cp ./src/ipa/raspberrypi/ipa_rpi.so $out/lib/libcamera
                  cp ../src/ipa/raspberrypi/data/*.json $out/share/libcamera/ipa/raspberrypi
                  cp -a ../include/libcamera include/libcamera $out/include
                  patchelf --set-rpath "/lib" `find $out/lib -type f`
                '';

                strictDeps = true;
                enableParallelBuilding = true;

                allowedReferences = [ ];
              };
            camera-driver = let pkgs = crosspkgs; in pkgs.stdenv.mkDerivation {
              name = "camera-driver";

              dontUnpack = true;

              nativeBuildInputs = with pkgs.buildPackages; [
                binutils
              ];

              installPhase = ''
                mkdir -p $out/lib/libcamera
                LIBCAM="${self.packages.${system}.libcamera}"
                chmod +w `find $out/`
                prefix=${pkgs.stdenv.targetPlatform.config}
                cclib="${pkgs.stdenv.cc.cc.lib}/$prefix/lib"

                cp "$cclib/libstdc++.so.6" \
                  "$cclib/libgcc_s.so.1" \
                  "$cclib/libatomic.so.1" \
                  $out/lib/
                chmod +w `find $out/`
                patchelf --set-rpath "/lib" `find $out/lib -type f`
                # Copy the dynamic link while stripping it of indeterministic sections.
                "$prefix"-objcopy -R .note.gnu.build-id "${pkgs.stdenv.cc.libc}/lib/${loader-lib}" \
                  $out/lib/${loader-lib}
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
            initramfs = self.lib.${system}.mkinitramfs false;
            initramfs-debug = self.lib.${system}.mkinitramfs true;
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
            reload-fast =
              let
                libcamera = self.packages.${system}.libcamera;
                pkgs = localpkgs;
              in
              pkgs.writeShellScriptBin "reload" ''
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
                CGO_CXXFLAGS="-I${libcamera}/include" \
                CGO_LDFLAGS="-L${libcamera}/lib" \
                go build -buildmode pie -ldflags="-w -s" -trimpath -tags debug,netgo -o "$PROG" ./cmd/controller
                patchelf --set-rpath "/lib" --set-interpreter "/lib/${loader-lib}" "$PROG"

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
              START=$(${self.packages.${system}.util-linux}/bin/fdisk -l -o Start $src|tail -n 1)
              OFFSET=$(( $START*512 ))
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
            default = self.packages.${system}.image;
          };
        # Build a shell capable of running #reload-fast.
        devShells.default = crosspkgs.go_1_20;
      });
}

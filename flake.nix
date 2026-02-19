{
  description = "Seedhammer firmware";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/25.11";
    utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      utils,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        # Raspberry Pi openocd fork with rp2350 support.
        openocd-overlay = final: prev: {
          openocd = prev.openocd.overrideAttrs (old: {
            pname = "openocd-rpi";
            src = final.fetchFromGitHub {
              owner = "raspberrypi";
              repo = "openocd";
              rev = "cf9c0b41cd5c45b2faf01b4fd1186f160342b7b7";
              hash = "sha256-Wqv9zGwyYgSk/5WqPYXnVWM+TQDJa9iqBQ3ev+o8aiA=";
              # openocd disables the vendored libraries that use submodules and replaces them with nix versions.
              # this works out as one of the submodule sources seems to be flaky.
              fetchSubmodules = false;
            };
            nativeBuildInputs = old.nativeBuildInputs ++ [
              final.autoreconfHook
            ];
            patches = [
              # https://github.com/raspberrypi/openocd/issues/120
              (final.writeText "sysresetreq-core1.patch" ''
                diff --git a/tcl/target/rp2350.cfg b/tcl/target/rp2350.cfg
                index 104e8ecc4..7dbcb759e 100644
                --- a/tcl/target/rp2350.cfg
                +++ b/tcl/target/rp2350.cfg
                @@ -77,3 +77,5 @@ if { $_BOTH_CORES } {

                 # srst does not exist; use SYSRESETREQ to perform a soft reset
                 cortex_m reset_config sysresetreq
                +# same for the second core, to avoid a warning.
                +rp2350.dap.core1 cortex_m reset_config sysresetreq
              '')
            ];
          });
        };
        # Newest TinyGo version.
        tinygo-overlay =
          let
            version = "0.41.0";
          in
          final: prev: {
            tinygo = prev.tinygo.overrideAttrs (prev: {
              version = "${version}";
              doCheck = false; # TinyGo tests are slow.
              src = final.fetchFromGitHub {
                owner = "tinygo-org";
                repo = "tinygo";

                rev = "fd1d10c9b7b6eba6168a0c03bb38c973da42ea1d";
                hash = "sha256-bpcApOSbqh5xNUxC+iYDB7PVxJdgSPWfPJArHtiPNNY=";

                fetchSubmodules = true;
                postFetch = ''
                  rm -r $out/lib/cmsis-svd/data/{SiliconLabs,Freescale}
                '';
              };
              vendorHash = "sha256-+962anRjsh1N0QHgEQIL8Dqwwsbps+LLEDpqCFBHksM=";
            });
          };
        # Full pico SDK for picotool seal support.
        pico-sdk-full = final: prev: {
          withSubmodules = true;
        };
        pkgs = import nixpkgs {
          inherit system;
          overlays = [
            tinygo-overlay
            openocd-overlay
            pico-sdk-full
          ];
        };
      in
      {
        formatter = pkgs.nixpkgs-fmt;
        packages = {
        }
        // (
          let
            tinygo-flags = "-target pico-plus2 -stack-size 16kb -gc precise -opt 2 -scheduler tasks";
          in
          {
            build-firmware = pkgs.writeShellScriptBin "build-firmware" ''
              set -eu -o pipefail

              if [ -z "''${VERSION:-}" ]; then
                VERSION=$(${pkgs.git}/bin/git describe --tags --always --dirty)
              fi
              WORKDIR="$(mktemp -d)"
              OUTPUT="seedhammerii-$VERSION.uf2"

              ${pkgs.tinygo}/bin/tinygo build -o "$WORKDIR/firmware.uf2" -ldflags="-X main.Version=$VERSION" ${tinygo-flags} "$@" ./cmd/controller
              # Sign with a dummy key to convince picotool to create the necessary
              # file structure for signing.
              ${pkgs.openssl}/bin/openssl ecparam -genkey -name secp256k1 -out "$WORKDIR/dummy.pem"
              ${pkgs.picotool}/bin/picotool seal --sign --clear --quiet "$WORKDIR/firmware.uf2" "$WORKDIR/firmware.signed.uf2" "$WORKDIR/dummy.pem"
              # Clear public key and signature.
              ${pkgs.go}/bin/go run seedhammer.com/cmd/picosign sign -clear "$WORKDIR/firmware.signed.uf2"

              mv "$WORKDIR/firmware.signed.uf2" "$OUTPUT"
              rm -rf "$WORKDIR"
              echo "Built $OUTPUT"
            '';
            flash-firmware = pkgs.writeShellScriptBin "flash-firmware" ''
              set -eu -o pipefail

              VERB="$1"; shift
              VERSION=$(${pkgs.git}/bin/git describe --dirty)
              ${pkgs.tinygo}/bin/tinygo "$VERB" -ldflags="-X main.Version=$VERSION" ${tinygo-flags} \
                -programmer=cmsis-dap -ocd-commands "adapter speed 15000" \
                -serial=uart "$@" \
                ./cmd/controller
            '';
            copy-signature = pkgs.writeShellScriptBin "copy-signature" ''
              set -eu -o pipefail

              PUBKEY="03bcea00fedcd40ddbb67f4a3f2fafb59bb942e7b2130a76ffbed5568497477bfd"
              SRC="$1"; shift
              DST="$1"; shift
              SIGNATURE=$(${pkgs.go}/bin/go run seedhammer.com/cmd/picosign extract "$SRC")
              ${pkgs.go}/bin/go run seedhammer.com/cmd/picosign sign -pubkey "$PUBKEY" -sig "$SIGNATURE" "$DST"
              echo "wat" $?
            '';
          }
        );
        devShells =
          let
            selfpkgs = self.packages.${system};
          in
          {
            default = pkgs.mkShell {
              packages = with pkgs; [
                tinygo
                gcc-arm-embedded
                go
                openocd
                pioasm
                picotool
                selfpkgs.build-firmware
                selfpkgs.flash-firmware
                selfpkgs.copy-signature
              ];
            };
          };
      }
    );
}

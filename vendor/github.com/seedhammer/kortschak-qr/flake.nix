{
  description = "development environment";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=25.05";
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
        pkgs = import nixpkgs {
          inherit system;
        };
        qrencode400 = pkgs.qrencode.overrideAttrs (old: rec {
          # Tests fail with newer libqrencode versions.
          version = "4.0.2";
          src = pkgs.fetchFromGitHub {
            owner = "fukuchi";
            repo = "libqrencode";
            rev = "v${version}";
            sha256 = "sha256-U2vv4RJtrfoLcxkP0H9eK0fLTu/aCv02O36sbPPrDno=";
          };
          nativeBuildInputs = [
            pkgs.pkg-config
            pkgs.automake
            pkgs.autoconf
            pkgs.autoreconfHook 
          ];
        });
      in
      {
        devShells = {
          default = pkgs.mkShell {
            packages = [
              qrencode400
            ];
          };
        };
      }
    );
}

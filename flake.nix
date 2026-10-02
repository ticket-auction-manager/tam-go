{
  description = "Ticket Auction Manager in Go: tam-server and tam-client";

  # Go 1.27 comes from here; the NixOS a machine runs can be older.
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forEachSystem = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forEachSystem (pkgs: {
        default = pkgs.callPackage ./nix/package.nix {
          stamp = self.shortRev or self.dirtyShortRev or "0.0.1";
        };
      });

      apps = forEachSystem (
        pkgs:
        let
          tam = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
          app = program: description: {
            type = "app";
            program = "${tam}/bin/${program}";
            meta = { inherit description; };
          };
        in
        {
          default = app "tam-client" "The client: the web app on http://localhost:3080/";
          tam-client = app "tam-client" "The client: the web app on http://localhost:3080/";
          tam-server = app "tam-server" "The shared database of an event's clients";
        }
      );

      nixosModules.default = import ./nix/module.nix self;

      checks = forEachSystem (
        pkgs:
        {
          package = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
        }
        // nixpkgs.lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
          nixos = pkgs.testers.runNixOSTest (import ./nix/test.nix self);
        }
      );

      devShells = forEachSystem (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go_1_27
            pkgs.nodejs_24
            pkgs.pnpm_12
          ];
        };
      });

      formatter = forEachSystem (pkgs: pkgs.nixfmt);
    };
}

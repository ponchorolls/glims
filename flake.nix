{
  description = "Go Lang Inventory Management System (glims) Dev Environment";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go
            gopls       # Go language server for your editor
            go-tools    # Static analysis tools
            sqlite      # To view your DB directly
          ];

          shellHook = ''
            echo "Welcome to the glims development shell!"
            export PS1="[glims-dev] $PS1"
          '';
        };
      });
}
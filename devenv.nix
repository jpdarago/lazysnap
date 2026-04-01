{ pkgs, ... }:

{
  packages = [
    pkgs.tarsnap
  ];

  languages.go.enable = true;
}

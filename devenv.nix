{ pkgs, ... }:

{
  packages = [
    pkgs.tarsnap
  ];

  languages.go.enable = true;

  env.GOFLAGS = "-mod=vendor";
}

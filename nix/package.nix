# tam-server and tam-client, built from this repository: the web app with
# pnpm first, then both Go programs, with the web app embedded in
# tam-client. The unit tests run as part of the build.
#
# When go.sum or frontend/pnpm-lock.yaml changes, the matching hash below
# changes too: set it to lib.fakeHash, run `nix build`, and copy the hash
# that Nix reports.
{
  lib,
  buildGo127Module,
  fetchPnpmDeps,
  nodejs_24,
  pnpm_12,
  pnpmConfigHook,
  stdenvNoCC,
  # The version both programs report (internal/version.Version); the flake
  # passes the commit, as build.sh stamps `git describe` without a tag.
  stamp ? "0.0.1",
}:

let
  version = "0.0.1";

  # What the build reads, without the folders a local build leaves behind.
  read = lib.fileset.unions [
    ../go.mod
    ../go.sum
    ../cmd
    ../internal
    ../frontend
  ];
  leftovers = lib.fileset.unions [
    (lib.fileset.maybeMissing ../frontend/node_modules)
    (lib.fileset.maybeMissing ../frontend/.svelte-kit)
    (lib.fileset.maybeMissing ../cmd/tam-client/dist)
  ];
  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.difference read leftovers;
  };

  # The web app, as `pnpm build` writes it into cmd/tam-client/dist.
  web = stdenvNoCC.mkDerivation (finalAttrs: {
    pname = "tam-web";
    inherit version src;

    pnpmRoot = "frontend";
    pnpmDeps = fetchPnpmDeps {
      inherit (finalAttrs) pname version src;
      pnpm = pnpm_12;
      sourceRoot = "${finalAttrs.src.name}/frontend";
      fetcherVersion = 4;
      hash = "sha256-c7SKTbiN44RXsmwHqlBzt3mZpuKTKC43ygOIU9sSxSs=";
    };

    nativeBuildInputs = [
      nodejs_24
      pnpm_12
      pnpmConfigHook
    ];

    buildPhase = ''
      runHook preBuild
      pnpm --dir frontend build
      runHook postBuild
    '';

    installPhase = ''
      runHook preInstall
      cp -r cmd/tam-client/dist $out
      runHook postInstall
    '';
  });
in
buildGo127Module {
  pname = "tam";
  inherit version src;

  vendorHash = "sha256-ABpeD1DwnbxMd+8Y+zYgEFi7LCsSbQkNZoV1FU32CRM=";

  env.CGO_ENABLED = 0;
  subPackages = [
    "cmd/tam-client"
    "cmd/tam-server"
  ];
  ldflags = [
    "-s"
    "-w"
    "-X ticket-auction-manager/tam-go/internal/version.Version=${stamp}"
  ];

  preBuild = ''
    rm -rf cmd/tam-client/dist
    cp -r ${web} cmd/tam-client/dist
    chmod -R u+w cmd/tam-client/dist
  '';

  # Every unit test, not only those of the two programs.
  checkPhase = ''
    runHook preCheck
    export HOME=$TMPDIR
    go test ./internal/...
    runHook postCheck
  '';

  postInstall = ''
    install -Dm644 cmd/tam-client/icon.svg $out/share/icons/hicolor/scalable/apps/tam-client.svg
    install -Dm644 cmd/tam-server/icon.svg $out/share/icons/hicolor/scalable/apps/tam-server.svg
  '';

  passthru = { inherit web; };

  meta = {
    description = "Ticket Auction Manager: tam-server holds an event's data, tam-client serves the web app on each client computer";
    homepage = "https://github.com/ticket-auction-manager/tam-go";
    license = lib.licenses.mit;
    mainProgram = "tam-client";
    platforms = lib.platforms.linux ++ lib.platforms.darwin;
  };
}

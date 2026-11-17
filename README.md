# IPA OTA Server

Serve an existing signed IPA over HTTP on a private network. Put a trusted HTTPS endpoint in front of the origin for over-the-air installation on iPhone and iPad. Build the standalone binary with Go 1.24 or later.

## Install the agent skill from this repository

Send [this GitHub repository](https://github.com/defbyAI/ipa-ota-server) to your agent for skill installation. The skill entry is [`skills/ipa-ota-server/SKILL.md`](skills/ipa-ota-server/SKILL.md) on the `main` branch. The repository includes a Codex marketplace with the same skill.

### Codex plugin

The commands below install the plugin `ipa-ota-server` from the `ipa-ota-server` marketplace.

```sh
repo_url=https://github.com/defbyAI/ipa-ota-server
codex plugin marketplace add "$repo_url"
codex plugin add ipa-ota-server@ipa-ota-server
```

In the Codex desktop app, add the repository as a plugin marketplace and select **IPA OTA Server**. Use the skill in a new session. [Codex plugin packaging and marketplaces](https://developers.openai.com/plugins/build/plugins)

### Standalone skill

Use the Codex skill installer with the repository URL and the skill directory. Set `skill_installer` to the installed `install-skill-from-github.py` helper.

```sh
python3 "$skill_installer" --url "$repo_url" --path skills/ipa-ota-server
```

The [direct skill directory URL](https://github.com/defbyAI/ipa-ota-server/tree/main/skills/ipa-ota-server) also works as the installer URL. Other Agent Skills clients can install that directory with their own skill installer. The directory includes the server and its HTML template.

## Publish and serve

Use an IPA already signed and provisioned for the intended devices. Device eligibility comes from the IPA's signing and provisioning. [Apple documentation for registered-device distribution](https://developer.apple.com/documentation/xcode/distributing-your-app-to-registered-devices)

From a clone of this repository, run the following with your IPA and a private publication directory.

```sh
ota=skills/ipa-ota-server/server
go -C "$ota" run . publish \
  --ipa /path/to/SignedApp.ipa \
  --directory /path/to/private/ota-public

go -C "$ota" run . serve --directory /path/to/private/ota-public --port 8787
```

Build a binary for a long-running deployment with `go -C skills/ipa-ota-server/server build -o /path/to/ipa-ota-server .`. The binary accepts the same subcommands and flags. The origin listens on `127.0.0.1` by default. Set `--bind` to the private interface address when the proxy runs on another machine.

The same publication works for HTTP downloads and HTTPS installation. Serve it at the HTTPS host's root path, and configure the proxy to preserve the incoming `Host`, including its port if present. Forward the paths and IPA Range headers to the HTTP origin. The origin builds each manifest's asset URLs from `https://` and the request's `Host`. Open the HTTPS page in Safari and tap **Install on iPhone or iPad**. [Apple Platform Deployment](https://support.apple.com/guide/deployment/distribute-proprietary-in-house-apps-depce7cefc4d/web)

Optional `--small-icon` and `--large-icon` arguments accept PNGs for the manifest and install page. `publish` reads the bundle identifier, version, build, and display name from the IPA and prints the SHA-256 digest and relative asset paths. Each release has its own paths. Publishing updates the page after the assets are ready, so an open install link can finish against its original files.

## Verify and migrate

- Check the HTTP page with `curl -fI http://127.0.0.1:8787/`. For OTA, fetch the manifest through the HTTPS proxy and check that its IPA URL uses the public host. Send `Range: bytes=0-31` to that IPA URL and check for `206` and `Content-Range`.
- For a cutover, publish to a separate directory on a spare port. Check the HTTPS endpoint and installation on a registered device before repointing the existing proxy. Keep the previous origin available for rollback during the cutover.
- Keep signed IPAs and generated site files in the private publication directory. The Git repository contains the server, skill, and synthetic tests.

Run the local tests with `go -C skills/ipa-ota-server/server test ./...`.

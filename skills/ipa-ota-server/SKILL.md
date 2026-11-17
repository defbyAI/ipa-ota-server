---
name: ipa-ota-server
description: Publish and serve an existing signed IPA for remote installation on iPhone or iPad. Use when staging IPA downloads over private HTTP, generating an HTTPS OTA manifest behind a reverse proxy, or updating a release on an existing IPA installation server.
---

# IPA OTA Server

Use the Go module under `server/` in this skill. Build or run it with Go 1.24 or later. Use an existing IPA signed and provisioned for the target device.

1. Identify the IPA, private publication directory, and intended HTTP bind address and port.
2. Run `go -C /path/to/skill/server run . publish --ipa /path/to/App.ipa --directory /path/to/publication`. Use absolute paths with `go -C`. Add `--small-icon` or `--large-icon` when the user supplies PNGs.
3. Run `go -C /path/to/skill/server run . serve --directory /path/to/publication --bind 127.0.0.1 --port 8787` when starting a new origin is in scope. Use the user's private bind address for a proxy that connects over a VPN. An existing origin serving that directory picks up published releases.
4. For OTA, put the origin behind a trusted HTTPS proxy at the root path. Preserve the incoming `Host` and forward the paths and Range requests. The origin builds the manifest from the `Host` it receives. Verify the public page, manifest, and IPA before reporting the install URL. Report device installation when verified on the device.

The install button appears over HTTPS. Use a small GET Range request to check IPA delivery. Store generated files and signed packages in the private publication directory and keep old releases available for devices still using their install links.

# IPA OTA Server

When the user supplies this repository URL for skill installation, the skill is `skills/ipa-ota-server`. Use the client's native skill installer. In Codex the built-in installer accepts `--url https://github.com/OWNER/REPO --path skills/ipa-ota-server`. Use the browser-style URL for a Git clone URL ending in `.git`. Honor a ref supplied in a GitHub tree URL. The default branch is `main`.

For Codex plugin installation, register the GitHub URL with `codex plugin marketplace add` and install `ipa-ota-server@ipa-ota-server`. The [README](README.md#install-the-agent-skill-from-this-repository) includes both entry points. An install request adds the skill files. Publishing or starting a server belongs to a deployment task.

The Go module and its embedded HTML template live in `skills/ipa-ota-server/server` so either installer delivers a self-contained copy. Keep deployment artifacts and signing material in the user's private publication directory. Run the tests with `go -C skills/ipa-ota-server/server test ./...`.

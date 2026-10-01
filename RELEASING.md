# Releasing Agentbus

Keep raw logs, admission tickets, runtime state and login files outside the
repository. Publish from the reviewed release branch with a public noreply
commit identity. Preserve private development history locally.

1. Run `make check`, `make scan`, `make docker-image`,
   `govulncheck ./...`, and `python3 tools/test-install.py`. Use a
   govulncheck binary built with the project's Go version or newer.
   Run the [local Docker NAT lab](docs/nat-testing.md). Its direct-recovery
   gate requires two same-connection recovery cycles. Rust-backed Linux
   qualification passes locally; native builds/tests for all four platforms
   must pass before publication.
2. Build versioned assets with:

   ```sh
   make release LDFLAGS="-X main.version=v0.4.0 -X main.commit=$(git rev-parse --short HEAD) -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
   ```

3. `make release` stamps the version from the Rust package manifest and
   packages the current native platform only. Run it on
   Linux/macOS amd64/arm64 (CI supplies native runners). Merge the four
   archives into one release directory and regenerate SHA256SUMS. Verify
   both executable versions and execute the transport tests on each runner.
4. Complete the real Claude/Codex wake/reply check. Record only
   a sanitized result in Git; keep addresses, credentials and transcripts
   private. State separately whether separate machines or distinct internet
   connections were tested.
5. Review the branch diff, new commit metadata, CHANGELOG.md, all four
   platform archives and SHA256SUMS before creating the v0.4.0 tag/release.
6. After publication, test the installer against that exact tag in a fresh
   destination and update the README release badge and candidate wording.

For transport migration, use the README's upgrading instructions. Public
n0 relays are for development/testing; production operators should configure
a dedicated HTTPS relay.

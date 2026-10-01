# 0006 — Official Rust Iroh owns network transport

**Status:** Accepted — 2026-09-30

Use unmodified official Rust Iroh 1.3.0 in the bundled `agentbus-iroh` helper.
The independent Go transport failed NAT recovery qualification, while official
Rust recovered in the same simulated topology. Maintaining a QUIC/NAT fork is
more costly than packaging a second executable.

Go keeps the hub, admission, signing, mailbox, tasks and runtime activation.
A private Unix socket bridges each bidirectional stream and preserves Go's
read/write deadlines. Helper configuration travels over inherited stdin; its
EOF ties helper lifetime to the parent. Existing host seeds and `ab1` tickets
remain valid. The existing Go ticket codec is retained solely for compatibility.

Releases bundle both executables and require native Linux/macOS builds on
amd64/arm64. The installer validates the pair and switches one symlink
atomically. Local Docker qualification does not establish every NAT type,
separate-provider traversal, or execution on untested release platforms.

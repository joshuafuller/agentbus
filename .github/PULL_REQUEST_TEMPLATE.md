<!-- Thanks for contributing to agentbus. Keep PRs focused. -->

## What & why

<!-- What does this change, and what problem does it solve? -->

## Security & tradeoff

<!-- Does this touch names, --on-msg/shell construction, file permissions,
     the wire protocol, or admission? If so, what did you consider?
     Does it add durable state / a queue / identity? Justify against the
     working baseline (see CONTRIBUTING.md). -->

## Testing

<!-- How did you verify this? Unit tests? End-to-end activation run? -->

## Checklist

- [ ] `make check` passes (vet + race tests)
- [ ] `make scan` is clean (no secrets, no tickets)
- [ ] Behavioral changes include tests
- [ ] Relevant docs updated (README / docs / SECURITY)

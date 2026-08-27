# Architecture Decision Records

Short records of decisions that shape agentbus, kept in-tree so the
reasoning survives the people and the sessions that produced it.

One file per decision, `NNNN-kebab-title.md`, numbered in the order they
were *proposed*. Each states its status (`Proposed`, `Accepted`,
`Superseded by NNNN`), the context that forced the decision, the decision
itself, its consequences — including the unpleasant ones — and the
alternatives that were rejected and why.

A record is never edited to change what was decided. If the decision
changes, write a new ADR and mark the old one superseded. The point is to
be able to reconstruct *why* a past choice looked right at the time.

| ADR | Title | Status |
|-----|-------|--------|
| [0001](0001-adopt-a2a-for-task-semantics.md) | Adopt A2A for task semantics; own transport, admission, activation | Proposed |
| [0002](0002-rider-identity-signed-agent-cards.md) | Rider identity via per-rider keys and signed Agent Cards | Proposed |
| [0003](0003-addressed-delivery-primary.md) | Addressed delivery as the primary path; broadcast as observability | Proposed |

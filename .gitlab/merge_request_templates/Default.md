<!-- Thank you! A few notes to make the review fast. -->

### What

<!-- One paragraph describing the change. -->

### Why

<!-- Linked issue or motivation. -->

Closes #

### How

<!-- Implementation notes worth flagging. Optional. -->

### Tests

- [ ] Added/updated Go tests
- [ ] Added/updated Vitest / Playwright tests
- [ ] Manually tested (describe below)

### Documentation

- [ ] Touched docs in same PR
- [ ] CHANGELOG.md `[Unreleased]` entry added
- [ ] N/A — internal change

### Checklist

- [ ] `pnpm run lint` clean
- [ ] `cd backend && go vet ./... && staticcheck ./...` clean
- [ ] `pnpm run test` and `cd backend && go test -race ./...` pass
- [ ] No secrets / `.env` files committed
- [ ] Conventional Commit message

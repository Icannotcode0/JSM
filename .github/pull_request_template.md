## What

<!-- One or two sentences. What changes, and why now. -->

## Notes for review

<!-- Anything a reviewer would otherwise have to reverse-engineer: a decision
     with a trade-off, a deliberate omission, something you're unsure about. -->

## Checklist

- [ ] `API.md` updated if an endpoint was added, removed, or changed shape
- [ ] Tests cover the new behaviour, including the failure paths
- [ ] No secrets, `.env` files, or real credentials in the diff
- [ ] Any new endpoint is on the correct side of the public/authenticated split
      in `internal/http/routers.go`

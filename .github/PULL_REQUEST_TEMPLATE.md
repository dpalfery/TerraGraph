## Summary

<!-- What changed and why. If it fixes a wrong answer, say which one. -->

## Test plan

- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `gofmt -l ./cmd ./internal` prints nothing
- [ ] `go mod tidy` leaves `go.mod` and `go.sum` unchanged
- [ ] Manual checks (describe if any):

## Checklist

- [ ] Linked issue (if applicable): #
- [ ] Tests added or updated when behaviour changes
- [ ] Docs updated when user-facing behaviour changes
- [ ] No secrets or credentials in the diff

<!--
Targeting `develop` unless this fixes something already broken in a release.

Merging to `main` publishes a release automatically, with the patch version
incremented. Docs-only changes do not trigger one.
-->

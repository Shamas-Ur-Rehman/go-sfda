## Summary

Describe the user-facing change.

## Validation

- [ ] `go vet ./...`
- [ ] `go test ./...`
- [ ] `go test -race ./...`
- [ ] `go build ./...`
- [ ] Updated documentation/changelog where needed

## Safety and compatibility

- [ ] Unit tests do not require live SFDA access
- [ ] No CAPTCHA, authentication, rate-limit, or other access-control bypass
- [ ] No generated datasets, checkpoints, credentials, or secrets included
- [ ] Public API and checkpoint compatibility impacts are documented

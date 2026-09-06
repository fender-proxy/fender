# Release v0.3.1

**Date:** September 6, 2026
**Type:** Patch/Revision Release

## Security Fixes

### Dependency Updates
- **Go Version:** Updated from 1.25.9 to 1.26.6 with latest security patches
- **golang.org/x/net:** Updated to v0.58.0 (security fixes for network operations)
- **golang.org/x/crypto:** Updated to v0.55.0 (cryptographic security improvements)
- **google.golang.org/grpc:** Updated to v1.83.2 (gRPC security patches)
- **google.golang.org/protobuf:** Updated to v1.36.12 (protocol buffer security fixes)
- **moby/buildkit:** Updated from v0.31.1 to v0.32.2 (Docker/container security patches)
- **github.com/fsnotify/fsnotify:** Updated to v1.10.1 (filesystem monitoring security)
- **github.com/spf13/cobra:** Updated to v1.10.2 (CLI framework updates)

### Workflow Security Enhancements
- **GitHub Actions Hardening:** Added explicit `permissions: contents: read` to CI workflows to follow principle of least privilege
- **E2E Testing:** Added comprehensive end-to-end testing workflow for enhanced validation

## What's Improved

✅ All critical and high-priority security vulnerabilities from dependencies patched
✅ Improved build reliability with latest buildkit version
✅ Enhanced GitHub Actions workflow security with explicit permission restrictions
✅ Comprehensive E2E test coverage for Docker socket proxy functionality
✅ OpenTelemetry dependencies updated for better observability

## Migration Notes

No breaking changes. This is a drop-in replacement for v0.3.0 with security improvements only.

## Testing

All existing functionality has been preserved. E2E tests verify:
- Docker socket proxy functionality
- Registry rewriting with BuildKit
- Configuration loading and startup
- Context registration

**Full Changelog:** https://github.com/fender-proxy/fender/compare/v0.3.0...v0.3.1

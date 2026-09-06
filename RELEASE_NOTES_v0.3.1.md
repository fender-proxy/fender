# Release v0.3.1 - Security Patch

**Date:** September 6, 2026  
**Type:** Patch/Revision Release - Security Fixes

## Overview

This is a security-focused patch release that addresses vulnerabilities in dependencies and hardens GitHub Actions workflows. All critical and high-priority security issues have been resolved with no breaking changes.

## Security Fixes

### Critical Dependency Updates

#### Cryptography & Networking
- **golang.org/x/crypto** → v0.55.0 (from v0.2.0)
  - Security fixes for cryptographic operations
  
- **golang.org/x/net** → v0.58.0 (from v0.56.0)
  - Network security patches
  - TLS/SSL improvements
  
- **golang.org/x/sys** → v0.47.0 (from v0.46.0)
  - System call security hardening

#### RPC & Protocol Buffer Security
- **google.golang.org/grpc** → v1.83.2 (from v1.81.1)
  - gRPC security patches and vulnerability fixes
  
- **google.golang.org/protobuf** → v1.36.12 (from v1.36.11)
  - Protocol buffer security improvements

#### Container & Build Security
- **moby/buildkit** → v0.32.2 (from v0.31.1)
  - Docker BuildKit security patches
  - Container image security enhancements
  - BuildKit API security fixes

#### Go Runtime
- **Go Version** → 1.26.6 (from 1.25.9)
  - Latest security patches from Go team
  - Performance improvements
  - Vulnerability fixes in stdlib

#### Additional Security Updates
- **fsnotify** → v1.10.1 (filesystem monitoring security)
- **spf13/cobra** → v1.10.2 (CLI framework hardening)
- **containerd** → v2.3.3 (container runtime security)
- **secure-systems-lab** → v0.11.0 (security library updates)
- **in-toto/in-toto-golang** → v0.11.0 (supply chain security)

### GitHub Actions Security Hardening

✅ **Explicit Permission Restrictions**
- Added `permissions: contents: read` to all CI workflows
- Follows principle of least privilege
- Reduces attack surface for GitHub Actions

✅ **End-to-End Testing Workflow**
- New E2E test workflow for enhanced validation
- Comprehensive Docker socket proxy testing
- Registry rewriting verification
- BuildKit integration validation

## What's Improved

- ✅ All critical CVEs resolved
- ✅ Latest cryptographic libraries
- ✅ Enhanced container security
- ✅ Improved gRPC security
- ✅ Hardened CI/CD pipeline
- ✅ Better observability with updated OpenTelemetry

## Compatibility

**Breaking Changes:** None

This is a drop-in replacement for v0.3.0. All APIs and functionality remain compatible.

## Testing

All security updates have been validated with:
- Unit test suite (passing)
- End-to-end integration tests (passing)
- Container image tests (passing)
- BuildKit compatibility tests (passing)

## Deployment

No special migration steps required. Simply update to v0.3.1 for all security benefits.

```bash
docker pull ghcr.io/fender-proxy/fender:v0.3.1
```

## Security Considerations

If you have strict security policies around dependency updates:
- All updates are to patch/minor versions only
- No breaking API changes
- All dependencies have been thoroughly tested
- Recommended to update immediately

## Next Steps

- 🔍 Review the [full dependency diff](https://github.com/fender-proxy/fender/compare/v0.3.0...v0.3.1)
- 📋 See the [detailed commit changelog](https://github.com/fender-proxy/fender/commits/v0.3.1)
- 🐛 Report any issues to [GitHub Issues](https://github.com/fender-proxy/fender/issues)

---

**Full Changelog:** https://github.com/fender-proxy/fender/compare/v0.3.0...v0.3.1

// Package image provides utilities for parsing and rewriting Docker image
// reference strings according to configured registry rules.
package image

import "strings"

// HasExplicitRegistry reports whether ref contains an explicit registry
// hostname. Per Docker conventions, the first slash-delimited path component
// is treated as a registry if it:
//   - contains a '.' (e.g. ghcr.io, registry.example.com)
//   - contains a ':' (e.g. localhost:5000)
//   - equals the literal string "localhost"
//
// Examples:
//
//	HasExplicitRegistry("nginx")            → false
//	HasExplicitRegistry("myorg/app:v1")     → false  (myorg has no . or :)
//	HasExplicitRegistry("ghcr.io/org/app")  → true
//	HasExplicitRegistry("localhost:5000/x") → true
func HasExplicitRegistry(ref string) bool {
	// Strip digest (@sha256:…) for analysis — it doesn't affect registry detection.
	name := ref
	if i := strings.Index(name, "@"); i != -1 {
		name = name[:i]
	}

	i := strings.Index(name, "/")
	if i == -1 {
		// Bare name like "nginx" or "nginx:1.25" — no registry component.
		return false
	}

	first := name[:i]
	return strings.ContainsAny(first, ".:") || first == "localhost"
}

// Rewrite rewrites an image reference string according to the provided rules.
//
// Important: the Docker CLI normalizes bare image names to their fully-qualified
// form before making API calls. For example, "nginx:latest" becomes
// "docker.io/library/nginx:latest" and "myorg/app:v1" becomes
// "docker.io/myorg/app:v1" by the time the request reaches the proxy.
// Rewrite accounts for this by treating docker.io as the implicit source
// whenever defaultRegistry is set.
//
// Rules applied in order:
//
//  1. Build an effective registry map:
//     • Start with a copy of registryMap.
//     • If defaultRegistry is set and "docker.io" is not already in the map,
//       add docker.io → defaultRegistry. This handles the Docker CLI's
//       implicit normalization of unqualified images.
//
//  2. If ref has no explicit registry and defaultRegistry is set,
//     prepend defaultRegistry (handles the rare case where an unqualified
//     reference is sent directly without CLI normalization).
//
//  3. Apply the effective registry map to replace any matching source registry.
//
// If no rule produces a change, ref is returned unchanged.
func Rewrite(ref, defaultRegistry string, registryMap map[string]string) string {
	if ref == "" {
		return ref
	}

	// Build the effective map: start from registryMap, inject docker.io →
	// defaultRegistry if not already present. We clone to avoid mutating the
	// caller's map.
	effective := make(map[string]string, len(registryMap)+1)
	for k, v := range registryMap {
		effective[k] = v
	}
	if defaultRegistry != "" {
		if _, exists := effective["docker.io"]; !exists {
			effective["docker.io"] = defaultRegistry
		}
	}

	if !HasExplicitRegistry(ref) {
		if defaultRegistry != "" {
			// Bare/unqualified name sent without CLI normalization.
			ref = defaultRegistry + "/" + ref
		} else if target, ok := effective["docker.io"]; ok {
			// No defaultRegistry but docker.io is explicitly in the map.
			if strings.Contains(ref, "/") {
				ref = target + "/" + ref
			} else {
				ref = target + "/library/" + ref
			}
			return ref
		}
	} else {
		// Image already has an explicit registry — apply the effective map.
		ref = applyMap(ref, effective)
	}

	return ref
}

// applyMap replaces the leading registry component of ref if it appears as a
// key in m. If no match is found, ref is returned unchanged.
func applyMap(ref string, m map[string]string) string {
	i := strings.Index(ref, "/")
	if i == -1 {
		return ref
	}
	src := ref[:i]
	if dst, ok := m[src]; ok {
		return dst + ref[i:]
	}
	return ref
}

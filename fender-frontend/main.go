package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	pb "github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/appcontext"
)

func main() {
	if err := grpcclient.RunFromEnvironment(appcontext.Context(), build); err != nil {
		fmt.Fprintf(os.Stderr, "fender-frontend fatal error: %+v\n", err)
		os.Exit(1)
	}
}

func build(ctx context.Context, c client.Client) (*client.Result, error) {
	opts := c.BuildOpts().Opts

	// Determine the filename of the Dockerfile (defaults to "Dockerfile")
	filename := opts["filename"]
	if filename == "" {
		filename = "Dockerfile"
	}

	// 1. Create a dockerui client to read the entrypoint (Dockerfile)
	bc, err := dockerui.NewClient(c)
	if err != nil {
		return nil, fmt.Errorf("creating dockerui client: %w", err)
	}

	dockerfileBytes, err := bc.ReadEntrypoint(ctx, filename)
	if err != nil {
		return nil, fmt.Errorf("reading Dockerfile: %w", err)
	}

	// 2. Extract mappings and config passed by proxy
	defaultRegistry := opts["fender-default-registry"]
	registryMapJSON := opts["fender-registry-map"]
	var registryMap map[string]string
	if registryMapJSON != "" {
		_ = json.Unmarshal([]byte(registryMapJSON), &registryMap)
	}

	// 3. Rewrite Dockerfile FROM instructions
	rewrittenBytes := rewriteDockerfile(dockerfileBytes.Data, defaultRegistry, registryMap)

	// 4. Build a synthetic LLB State containing the modified Dockerfile
	st := llb.Scratch().File(
		llb.Mkfile(filename, 0644, rewrittenBytes),
	)

	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, fmt.Errorf("marshaling synthetic Dockerfile state: %w", err)
	}

	// 5. Invoke the original Dockerfile frontend image
	originalFrontend := opts["fender-original-frontend"]
	if originalFrontend == "" {
		originalFrontend = "docker/dockerfile:1" // Fallback to standard
	}

	// Construct options, removing fender internal options to keep clean
	nestedOpts := make(map[string]string)
	for k, v := range opts {
		nestedOpts[k] = v
	}
	delete(nestedOpts, "source")
	delete(nestedOpts, "fender-original-frontend")
	delete(nestedOpts, "fender-default-registry")
	delete(nestedOpts, "fender-registry-map")

	// Set original/standard gateway options
	req := client.SolveRequest{
		Frontend:    "gateway.v0",
		FrontendOpt: nestedOpts,
		FrontendInputs: map[string]*pb.Definition{
			"dockerfile": def.ToPB(),
		},
	}
	req.FrontendOpt["source"] = originalFrontend

	res, err := c.Solve(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("solving nested frontend: %w", err)
	}

	return res, nil
}

// rewriteDockerfile replaces the base image registries in the FROM lines
func rewriteDockerfile(content []byte, defaultRegistry string, registryMap map[string]string) []byte {
	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(trimmed), "FROM") {
			continue
		}

		words := strings.Fields(line)
		if len(words) < 2 {
			continue
		}

		// Find the base image token (first token after FROM that doesn't start with "--")
		imgIdx := -1
		for j := 1; j < len(words); j++ {
			if !strings.HasPrefix(words[j], "--") {
				imgIdx = j
				break
			}
		}
		if imgIdx == -1 {
			continue
		}

		origImg := words[imgIdx]

		// Skip if it appears to be a build ARG reference (e.g., $BASE_IMAGE or ${BASE_IMAGE})
		if strings.Contains(origImg, "$") {
			continue
		}

		rewrittenImg := rewriteReference(origImg, defaultRegistry, registryMap)
		if rewrittenImg == origImg {
			continue
		}

		// Reconstruct the line preserving the leading indentation
		words[imgIdx] = rewrittenImg
		leadingWs := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		lines[i] = leadingWs + strings.Join(words, " ")
	}
	return []byte(strings.Join(lines, "\n"))
}

// Simple implementation of image.Rewrite to keep the frontend completely standalone and self-contained
func rewriteReference(ref, defaultRegistry string, registryMap map[string]string) string {
	if ref == "" {
		return ref
	}

	effective := make(map[string]string, len(registryMap)+1)
	for k, v := range registryMap {
		effective[k] = v
	}
	if defaultRegistry != "" {
		if _, exists := effective["docker.io"]; !exists {
			effective["docker.io"] = defaultRegistry
		}
	}

	if !hasExplicitRegistry(ref) {
		if defaultRegistry != "" {
			ref = defaultRegistry + "/" + ref
		} else if target, ok := effective["docker.io"]; ok {
			if strings.Contains(ref, "/") {
				ref = target + "/" + ref
			} else {
				ref = target + "/library/" + ref
			}
			return ref
		}
	} else {
		ref = applyMap(ref, effective)
	}

	return ref
}

func hasExplicitRegistry(ref string) bool {
	name := ref
	if i := strings.Index(name, "@"); i != -1 {
		name = name[:i]
	}
	i := strings.Index(name, "/")
	if i == -1 {
		return false
	}
	first := name[:i]
	return strings.ContainsAny(first, ".:") || first == "localhost"
}

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

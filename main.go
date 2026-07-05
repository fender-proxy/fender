package main

import "github.com/fender-proxy/fender/cmd"

// version is overridden at build time via:
//
//	go build -ldflags "-X main.version=1.0.0"
var version = "dev"

func main() {
	cmd.SetVersion(version)
	cmd.Execute()
}

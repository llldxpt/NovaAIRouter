package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

var (
	gatewayBin  = flag.String("gateway-bin", "novaairouter.exe", "Path to novaairouter binary")
	gatewayDir  = flag.String("gateway-dir", ".", "Working directory for gateway")
	pluginBin   = flag.String("plugin-bin", "models_plugin/model-router-plugin.exe", "Path to plugin binary")
	pluginDir   = flag.String("plugin-dir", "models_plugin", "Working directory for plugin")
	listenAddr  = flag.String("listen", ":15048", "Listen address for management web UI")
)

func main() {
	flag.Parse()

	// Resolve paths relative to executable or working dir
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		// Try relative to working directory first
		if _, err := os.Stat(p); err == nil {
			abs, _ := filepath.Abs(p)
			return abs
		}
		return p
	}

	gwBin := resolve(*gatewayBin)
	gwDir := resolve(*gatewayDir)
	plBin := resolve(*pluginBin)
	plDir := resolve(*pluginDir)

	log.Printf("Gateway binary: %s", gwBin)
	log.Printf("Gateway workdir: %s", gwDir)
	log.Printf("Plugin binary: %s", plBin)
	log.Printf("Plugin workdir: %s", plDir)

	server := NewManagerServer(gwBin, gwDir, plBin, plDir)

	fmt.Printf("\n  Route Manager listening on http://localhost%s\n\n", *listenAddr)
	log.Fatal(server.Start())
}

package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
)

var (
	configFile = flag.String("config", "config.json", "Path to configuration file")
	showHelp   = flag.Bool("help", false, "Show help message")
)

func main() {
	flag.Parse()

	if *showHelp {
		flag.Usage()
		return
	}

	cfg, err := LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	plugin := NewPlugin(cfg)

	if err := plugin.Start(); err != nil {
		log.Fatalf("Failed to start plugin: %v", err)
	}

	log.Printf("Model Router Plugin started on %s", cfg.PluginAddr)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down plugin...")
	plugin.Stop()
}

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

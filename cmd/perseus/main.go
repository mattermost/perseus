package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/mattermost/perseus/config"
	"github.com/mattermost/perseus/internal/server"
)

func main() {
	var configFile string
	flag.StringVar(&configFile, "config", "config/config.json", "Configuration file for the Perseus service.")
	flag.Parse()

	cfg, err := config.Parse(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not parse config file: %s\n", err)
		os.Exit(1)
	}

	s, err := server.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not start the server: %s\n", err)
		os.Exit(1)
	}
	sigShutdown := make(chan os.Signal, 1)
	signal.Notify(sigShutdown, os.Interrupt, syscall.SIGTERM)

	sigReload := make(chan os.Signal, 1)
	signal.Notify(sigReload, syscall.SIGHUP)

	var stopped atomic.Bool

	go func() {
		err := s.AcceptConns()
		// No need to print error if server is manually stopping.
		if err != nil && !stopped.Load() {
			fmt.Println(err)
		}
	}()
	defer s.Stop()

	var reloadWg sync.WaitGroup
	reloadWg.Add(1)
	go func() {
		defer reloadWg.Done()
		for range sigReload {
			cfg, err := config.Parse(configFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "could not parse config file: %s\n", err)
				continue
			}
			s.Reload(cfg)
		}
	}()

	// Wait for shutdown signal
	<-sigShutdown

	// Shut down the config reload loop
	signal.Stop(sigReload)
	close(sigReload)
	reloadWg.Wait()

	// Mark as stopped
	stopped.Store(true)
}

package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"tunnelfy/internal/ssh"
)

func main() {
	// Define command-line flags.
	serverAddr := flag.String("server", "localhost:2222", "SSH server address (e.g., localhost:2222)")
	username := flag.String("user", "", "SSH username for authentication")
	keyPath := flag.String("key", "", "Path to the private SSH key file")
	localAddr := flag.String("local", "localhost:3000", "Local service address to forward (e.g., localhost:3000)")
	verbose := flag.Bool("v", false, "Enable verbose logging")

	flag.Parse()

	// Validate required flags.
	if *username == "" {
		log.Fatal("Error: -user flag is required")
	}
	if *keyPath == "" {
		log.Fatal("Error: -key flag is required")
	}

	// Configure the SSH client.
	var logger *log.Logger
	if *verbose {
		logger = log.New(os.Stderr, "SSHClient: ", log.LstdFlags|log.Lmsgprefix)
	} else {
		logger = log.New(os.Stderr, "", 0) // Discard logs if not verbose
	}

	config := ssh.ClientConfig{
		ServerAddress:      *serverAddr,
		Username:          *username,
		KeyPath:           *keyPath,
		LocalServiceAddress: *localAddr,
		Logger:            logger,
	}

	// Create and connect the SSH client.
	client := ssh.NewClient(config)
	logger.Printf("Starting tunnelfy-client...")
	logger.Printf("  Server: %s", *serverAddr)
	logger.Printf("  Username: %s", *username)
	logger.Printf("  Key: %s", *keyPath)
	logger.Printf("  Local: %s", *localAddr)

	assignedPort, err := client.Connect()
	if err != nil {
		logger.Fatalf("Failed to connect: %v", err)
	}

	logger.Printf("✅ Tunnel established successfully!")
	logger.Printf("   Remote port assigned by server: %d", assignedPort)
	logger.Printf("   Press Ctrl+C to stop the client.")

	// Set up a channel to listen for OS interrupt signals.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Block until a signal is received.
	<-sigChan
	logger.Println("🛑 Interrupt signal received. Shutting down...")

	// Close the client connection gracefully.
	if err := client.Close(); err != nil {
		logger.Printf("❌ Error closing client: %v", err)
	} else {
		logger.Println("✅ Client stopped gracefully.")
	}
}

package ssh

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

// ClientConfig holds the configuration for the SSH tunnel client.
type ClientConfig struct {
	// ServerAddress is the address of the SSH server (e.g., "localhost:2222").
	ServerAddress string
	// Username is the SSH username for authentication.
	Username string
	// KeyPath is the path to the private SSH key file.
	KeyPath string
	// LocalServiceAddress is the address of the local service to forward (e.g., "localhost:3000").
	LocalServiceAddress string
	// Logger is an optional logger for client messages.
	Logger *log.Logger
}

// Client represents an SSH tunnel client.
type Client struct {
	config ClientConfig
	conn   *ssh.Client
}

// NewClient creates a new SSH tunnel client.
func NewClient(config ClientConfig) *Client {
	if config.Logger == nil {
		config.Logger = log.New(os.Stderr, "SSHClient: ", log.LstdFlags|log.Lmsgprefix)
	}
	return &Client{config: config}
}

// Connect establishes an SSH connection and requests a remote port forward.
// It blocks until the connection is established or an error occurs.
// The caller should handle disconnections and potentially call Connect again.
func (c *Client) Connect() (assignedRemotePort uint32, err error) {
	c.config.Logger.Printf("Attempting to connect to %s as %s", c.config.ServerAddress, c.config.Username)

	// Load the private key.
	key, err := os.ReadFile(c.config.KeyPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read private key file %s: %w", c.config.KeyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return 0, fmt.Errorf("failed to parse private key: %w", err)
	}

	// SSH client configuration.
	sshConfig := &ssh.ClientConfig{
		User:            c.config.Username,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Production-ready should use a known_hosts mechanism
		// Add a timeout for the initial handshake.
		Timeout: 15 * time.Second,
	}

	// Dial the SSH server.
	c.conn, err = ssh.Dial("tcp", c.config.ServerAddress, sshConfig)
	if err != nil {
		return 0, fmt.Errorf("failed to dial SSH server: %w", err)
	}
	c.config.Logger.Printf("Successfully connected to SSH server %s", c.config.ServerAddress)

	// Request remote port forwarding for port 0 (dynamic allocation).
	// The payload for tcpip-forward is: uint32(addr_len) + addr_bytes + uint32(port)
	// We are forwarding to 0.0.0.0:0, but the server will interpret this as a request for any available port.
	// The actual target for the forwarding is specified by the client and is handled by the server.
	// For this client, we just need to request the port.
	addr := "0.0.0.0"
	addrBytes := []byte(addr)
	payload := new(bytes.Buffer)

	binary.Write(payload, binary.BigEndian, uint32(len(addrBytes)))
	payload.Write(addrBytes)
	binary.Write(payload, binary.BigEndian, uint32(0)) // Request port 0

	ok, replyPayload, err := c.conn.SendRequest("tcpip-forward", true, payload.Bytes())
	if err != nil {
		c.conn.Close()
		return 0, fmt.Errorf("failed to send tcpip-forward request: %w", err)
	}
	if !ok {
		c.conn.Close()
		return 0, errors.New("server rejected tcpip-forward request")
	}

	// The reply payload for a successful tcpip-forward is just the assigned port (uint32).
	if len(replyPayload) != 4 {
		c.conn.Close()
		return 0, fmt.Errorf("server returned malformed reply payload for tcpip-forward: %v", replyPayload)
	}

	assignedRemotePort = binary.BigEndian.Uint32(replyPayload[:4])
	c.config.Logger.Printf("Server assigned remote port: %d", assignedRemotePort)

	// The connection is now established and the port is forwarded.
	// The client should now listen for incoming connections on the remote port
	// and forward them to the local service. However, the `golang.org/x/crypto/ssh`
	// client library doesn't directly expose the listener for remote forwards.
	// The server-side (tunnelfy) handles the listening and forwarding.
	// This client's job is to maintain the connection so the tunnel stays active.
	// We can start a goroutine to monitor the connection and handle disconnects.
	go c.monitorConnection()

	return assignedRemotePort, nil
}

// monitorConnection keeps the SSH connection alive and handles disconnections.
func (c *Client) monitorConnection() {
	if c.conn == nil {
		return
	}

	// Wait for the connection to close.
	// This can happen due to network issues, server shutdown, etc.
	err := c.conn.Wait()
	if err != nil {
		c.config.Logger.Printf("SSH connection closed: %v", err)
	} else {
		c.config.Logger.Printf("SSH connection closed gracefully.")
	}
	// In a real-world application, you might want to implement a reconnection logic here.
	// For now, we just log the closure.
}

// Close gracefully closes the SSH connection.
func (c *Client) Close() error {
	c.config.Logger.Printf("Closing SSH connection...")
	if c.conn != nil {
		err := c.conn.Close()
		if err != nil {
			return fmt.Errorf("failed to close SSH connection: %w", err)
		}
		c.config.Logger.Printf("SSH connection closed successfully.")
		return nil
	}
	return errors.New("client is not connected")
}

// A simple example of how to use the client.
// This would typically be in a main package or another service.
/*
func main() {
	config := ssh.ClientConfig{
		ServerAddress:      "localhost:2222",
		Username:          "testuser",
		KeyPath:           "./test_key",
		LocalServiceAddress: "localhost:3000",
	}

	client := ssh.NewClient(config)
	assignedPort, err := client.Connect()
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	log.Printf("Tunnel established. Remote port: %d", assignedPort)
	log.Printf("Press Ctrl+C to stop the client.")

	// Wait for an interrupt signal to close the client.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	if err := client.Close(); err != nil {
		log.Printf("Error closing client: %v", err)
	}
	log.Println("Client stopped.")
}
*/

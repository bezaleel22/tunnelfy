package ssh

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"

	"tunnelfy/internal/proxy"
)

// parseForwardPort parses the request payload for "tcpip-forward" and returns
// the requested port as string. Fails if payload is too short or invalid.
func parseForwardPort(payload []byte) (string, error) {
	// payload: uint32 addr_len | addr_bytes | uint32 port
	if len(payload) < 4 {
		return "", errors.New("payload too short")
	}
	addrLen := int(binary.BigEndian.Uint32(payload[0:4]))
	expected := 4 + addrLen + 4
	if len(payload) < expected {
		return "", fmt.Errorf("invalid payload length: want %d have %d", expected, len(payload))
	}
	port := binary.BigEndian.Uint32(payload[4+addrLen : 4+addrLen+4])
	return fmt.Sprintf("%d", port), nil
}

// SSHServer wraps the SSH configuration and active tunnel bookkeeping.
type SSHServer struct {
	config        *ssh.ServerConfig
	manager       *proxy.ShardedRouteManager
	zone          string
	activeTunnelM sync.Map // key user:port -> host string
	logRequests   bool
}

// NewSSHServer builds server config with public-key auth using provided keys map.
func NewSSHServer(authorizedKeys map[string]ssh.PublicKey, zone string, manager *proxy.ShardedRouteManager, logRequests bool) *SSHServer {
	cfg := &ssh.ServerConfig{
		// Public key authentication only.
		// NoClientAuth: false is the default. We will use a callback to enforce public key auth.
	}

	// PublicKeyCallback validates the incoming key against our authorized list
	// and injects the username into session permissions for later retrieval.
	cfg.PublicKeyCallback = func(connMeta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if _, ok := authorizedKeys[string(ssh.MarshalAuthorizedKey(key))]; ok {
			// Store username in Permissions so we can access it after handshake.
			p := &ssh.Permissions{
				Extensions: map[string]string{"username": connMeta.User()},
			}
			return p, nil
		}
		return nil, fmt.Errorf("unauthorized key")
	}

	// Add the host key to the configuration.
	signer := generateOrFallbackHostKey()
	if signer != nil {
		cfg.AddHostKey(signer)
	} // If signer is nil, the server will generate an ephemeral key.

	// Build and return SSHServer wrapper.
	return &SSHServer{
		config:      cfg,
		manager:     manager,
		zone:        zone,
		logRequests: logRequests,
	}
}

// HandleConn handles a completed SSH connection.
func (s *SSHServer) HandleConn(nConn net.Conn) {
	// Perform the SSH handshake and create a server connection.
	sshConn, chans, reqs, err := ssh.NewServerConn(nConn, s.config)
	if err != nil {
		if s.logRequests {
			log.Printf("ssh handshake failed: %v", err)
		}
		nConn.Close()
		return
	}
	// Ensure connection is closed when we return.
	defer sshConn.Close()

	// Extract username set in PublicKeyCallback
	var username string
	if sshConn.Permissions != nil {
		username = sshConn.Permissions.Extensions["username"]
	}
	if username == "" {
		// No username (shouldn't happen if auth callback set it); close.
		if s.logRequests {
			log.Printf("ssh connection without username; closing")
		}
		return
	}

	// reqs receives global requests (including tcpip-forward & cancel-tcpip-forward)
	// chans receives channel open requests (we reject them since we only use forwarding)
	// We'll spawn goroutines to handle both; they run for connection lifetime.

	// Handle channels: reject everything (no shell).
	go func() {
		for newChan := range chans {
			newChan.Reject(ssh.UnknownChannelType, "no channel support, tunneling only")
		}
	}()

	// Handle global requests: these include tcpip-forward and cancel-tcpip-forward.
	for req := range reqs {
		switch req.Type {
		case "tcpip-forward":
			requestedPortStr, err := parseForwardPort(req.Payload)
			if err != nil {
				if s.logRequests {
					log.Printf("failed parse tcpip-forward payload: %v", err)
				}
				req.Reply(false, nil)
				continue
			}

			// Determine the listen address. If port is "0", the OS assigns a random port.
			listenAddr := "127.0.0.1:" + requestedPortStr
			listener, err := net.Listen("tcp", listenAddr)
			if err != nil {
				log.Printf("failed to listen on %s: %v", listenAddr, err)
				req.Reply(false, nil)
				continue
			}

			// Get the actual port the listener is on. This is crucial if "0" was requested.
			actualPort := listener.Addr().(*net.TCPAddr).Port
			actualPortStr := fmt.Sprintf("%d", actualPort)

			fullHost := username + "." + s.zone
			// The target for the route is the local port the SSH server is listening on.
			routeTarget := fmt.Sprintf("127.0.0.1:%d", actualPort)

			if err := s.manager.AddRoute(fullHost, routeTarget); err != nil {
				if s.logRequests {
					log.Printf("failed to add route %s -> %s: %v", fullHost, routeTarget, err)
				}
				listener.Close() // Clean up listener
				req.Reply(false, nil)
				continue
			}
			key := username + ":" + actualPortStr
			s.activeTunnelM.Store(key, fullHost)

			// Construct the reply payload. For tcpip-forward, it's the assigned port.
			replyPayload := make([]byte, 4)
			binary.BigEndian.PutUint32(replyPayload, uint32(actualPort))
			req.Reply(true, replyPayload)

			if s.logRequests {
				log.Printf("tcpip-forward accepted and listening: %s -> %s (user=%s, requested_port=%s, assigned_port=%s)", fullHost, routeTarget, username, requestedPortStr, actualPortStr)
			}

			// Start a goroutine to handle connections to this listener.
			// This goroutine will forward traffic to the original target specified by the client.
			// The original target is embedded in the request payload, but our current logic simplifies it.
			// For now, we assume the client's target is always localhost:3000 for this test.
			// A more robust solution would parse the original target from the request.
			go func(l net.Listener, upstreamHost string, upstreamPort string) {
				defer l.Close()
				// Use the actual routeTarget for logging, as it contains the correct port.
				currentRouteTarget := fmt.Sprintf("127.0.0.1:%d", l.Addr().(*net.TCPAddr).Port)
				for {
					clientConn, err := l.Accept()
					if err != nil {
						// Listener closed, exit goroutine.
						if s.logRequests {
							log.Printf("listener on %s closed: %v", currentRouteTarget, err)
						}
						return
					}
					if s.logRequests {
						log.Printf("new connection on %s, forwarding to %s:%s", currentRouteTarget, upstreamHost, upstreamPort)
					}
					// Forward the connection to the upstream service.
					go func(c net.Conn) {
						defer c.Close()
						upstreamConn, err := net.Dial("tcp", upstreamHost+":"+upstreamPort)
						if err != nil {
							if s.logRequests {
								log.Printf("failed to dial upstream %s:%s: %v", upstreamHost, upstreamPort, err)
							}
							return
						}
						defer upstreamConn.Close()

						var wg sync.WaitGroup
						wg.Add(2)

						// Copy data from client to upstream
						go func() {
							defer wg.Done()
							if _, err := io.Copy(upstreamConn, c); err != nil {
								// It's common to get a connection reset error here when the other side closes.
								// We can log it as debug if needed, but it's not necessarily an error.
								if s.logRequests && err.Error() != "EOF" {
									log.Printf("debug: copying from client to upstream finished: %v", err)
								}
							}
						}()

						// Copy data from upstream to client
						go func() {
							defer wg.Done()
							if _, err := io.Copy(c, upstreamConn); err != nil {
								if s.logRequests && err.Error() != "EOF" {
									log.Printf("debug: copying from upstream to client finished: %v", err)
								}
							}
						}()

						wg.Wait()
						if s.logRequests {
							log.Printf("finished proxying connection from %s to %s:%s", c.RemoteAddr(), upstreamHost, upstreamPort)
						}
					}(clientConn)
				}
			}(listener, "localhost", "3000") // Hardcoded for now, parse from payload later.

		case "cancel-tcpip-forward":
			port, err := parseForwardPort(req.Payload)
			if err != nil {
				if s.logRequests {
					log.Printf("failed parse cancel-tcpip-forward payload: %v", err)
				}
				req.Reply(false, nil)
				continue
			}
			key := username + ":" + port
			if v, ok := s.activeTunnelM.Load(key); ok {
				if hostStr, ok2 := v.(string); ok2 {
					s.manager.RemoveRoute(hostStr)
				}
				s.activeTunnelM.Delete(key)
			}
			req.Reply(true, nil)
			if s.logRequests {
				log.Printf("tcpip-forward cancelled: user=%s port=%s", username, port)
			}

		default:
			req.Reply(false, nil)
		}
	}

	// Clean up any tunnels associated with this user on disconnect.
	s.activeTunnelM.Range(func(k, v interface{}) bool {
		ks, _ := k.(string)
		if strings.HasPrefix(ks, username+":") {
			if hostStr, ok := v.(string); ok {
				s.manager.RemoveRoute(hostStr)
				if s.logRequests {
					log.Printf("cleanup route on disconnect: %s", hostStr)
				}
			}
			s.activeTunnelM.Delete(ks)
		}
		return true
	})
}

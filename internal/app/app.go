package app

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tunnelfy/internal/config"
	"tunnelfy/internal/proxy"
	"tunnelfy/internal/ssh"
)

// App represents the Tunnelfy application.
type App struct {
	cfg        *config.Config
	manager    *proxy.ShardedRouteManager
	sshServer  *ssh.SSHServer
	httpServer *http.Server
}

// New creates a new App instance.
func New() (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	manager := proxy.NewShardedRouteManager(cfg.LogRequests)

	authKeys, err := ssh.LoadAuthorizedKeys(cfg.AuthorizedKeys)
	if err != nil {
		return nil, err // Or wrap the error for more context
	}

	sshSrv := ssh.NewSSHServer(authKeys, cfg.Zone, manager, cfg.LogRequests)

	mux := http.NewServeMux()
	mux.HandleFunc("/", proxy.FastProxyHandler(manager, cfg.Zone))
	mux.HandleFunc("/api/routes", proxy.RoutesAPIHandler(manager)) // Note: RoutesAPIHandler should be exported

	httpServer := &http.Server{
		Addr:    cfg.HTTPListen,
		Handler: mux,
	}

	return &App{
		cfg:        cfg,
		manager:    manager,
		sshServer:  sshSrv,
		httpServer: httpServer,
	}, nil
}

// Start starts the SSH and HTTP servers.
func (a *App) Start() error {
	// Start SSH listener
	sshListener, err := net.Listen("tcp", a.cfg.SSHListen)
	if err != nil {
		return err
	}
	defer sshListener.Close()
	if a.cfg.LogRequests {
		log.Printf("SSH listening on %s", a.cfg.SSHListen)
	}

	sshDone := make(chan struct{})
	go func() {
		defer close(sshDone)
		for {
			nConn, err := sshListener.Accept()
			if err != nil {
				// If listener closed, exit accept loop
				if ne, ok := err.(net.Error); ok && ne.Temporary() {
					log.Printf("temporary ssh accept error: %v", err)
					time.Sleep(100 * time.Millisecond)
					continue
				}
				// Permanent error -> break
				if a.cfg.LogRequests {
					log.Printf("ssh accept error: %v", err)
				}
				return
			}
			// Handle connection in background
			go a.sshServer.HandleConn(nConn) // HandleConn should be exported
		}
	}()

	// Start HTTP server
	httpDone := make(chan struct{})
	go func() {
		defer close(httpDone)
		if a.cfg.LogRequests {
			log.Printf("HTTP proxy listening on %s", a.cfg.HTTPListen)
		}
		if err := a.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server error: %v", err) // Consider returning error instead of fatal
		}
	}()

	// Wait for shutdown signal
	a.waitForShutdown(sshListener, sshDone, httpDone)

	log.Println("shutdown complete")
	return nil
}

// waitForShutdown handles OS signals for graceful shutdown.
func (a *App) waitForShutdown(sshListener net.Listener, sshDone, httpDone chan struct{}) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("signal received: %v; shutting down", sig)

	// Close SSH listener to stop accept loop
	sshListener.Close()

	// Shutdown HTTP server with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = a.httpServer.Shutdown(ctx)

	// Wait for goroutines to finish
	<-sshDone
	<-httpDone
}

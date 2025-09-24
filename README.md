# Tunnelfy

Tunnelfy is a high-performance SSH tunnel and reverse proxy manager written in Go. It allows users to expose local services to the internet securely via SSH reverse tunneling, with a built-in HTTP reverse proxy to route incoming requests to the correct service based on the hostname.

## Features

- **SSH Reverse Tunneling**: Securely expose local ports to a remote server.
- **Dynamic HTTP Reverse Proxy**: Automatically routes `*.yourdomain.com` to the correct local service based on the SSH username.
- **High-Performance Routing**: Uses a sharded in-memory map for low-latency route lookups under high concurrency.
- **Public Key Authentication**: Secure SSH access using authorized keys.
- **Simple Configuration**: Easy setup via environment variables or a `.env` file.
- **Admin API**: A JSON endpoint at `/api/routes` to view active tunnels.
- **Graceful Shutdown**: Handles SIGINT and SIGTERM for clean termination.
- **Arbitrary Port Allocation**: Correctly handles SSH `-R 0:...` requests by dynamically assigning an available port and communicating it back to the client.

## How It Works

1.  A user establishes an SSH connection to the Tunnelfy server with their public key.
2.  The user then requests a remote port forward (e.g., `-R 0:localhost:3000`). Tunnelfy dynamically assigns a port on the server and informs the client.
3.  Tunnelfy captures this request and dynamically creates a route: `<username>.<zone>` -> `127.0.0.1:<assigned_port>`.
4.  When an HTTP request arrives at `http://<username>.<zone>`, the Tunnelfy reverse proxy forwards it to the user's local service via the established SSH tunnel.

## Getting Started

### Prerequisites

- Go 1.19 or later
- An SSH client (e.g., OpenSSH)

### Installation

1.  **Clone the repository:**
    ```bash
    git clone https://github.com/bezaleel22/tunnelfy.git
    cd tunnelfy
    ```

2.  **Build the application:**
    ```bash
    go build -o tunnelfy ./cmd/tunnelfy
    ```

### Configuration

Tunnelfy is configured using environment variables. You can create a `.env` file in the project root for convenience.

**Required Environment Variables:**

-   `AUTHORIZED_KEYS`: A comma-separated list of authorized public SSH keys for authentication.

**Optional Environment Variables:**

-   `ZONE`: The base domain for generated hostnames (default: `tunnelfy.test`). For instance, if `ZONE=tunnelfy.dev`, a user `alice` would be accessible at `alice.tunnelfy.dev`.
-   `SSH_LISTEN`: The address and port for the SSH server to listen on (default: `:2222`).
-   `HTTP_LISTEN`: The address and port for the HTTP reverse proxy to listen on (default: `:8000`).
-   `LOG_REQUESTS`: Set to `true` to enable detailed request logging (default: `false`).

**Example `.env` file:**

```env
# The base domain for your tunnels
ZONE=tunnelfy.test

# Address for the SSH server
SSH_LISTEN=:2222

# Address for the HTTP proxy
HTTP_LISTEN=:8000

# Comma-separated authorized public keys
AUTHORIZED_KEYS=ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQD... user1@machine,ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... user2@machine

# Enable request logging (true/false)
LOG_REQUESTS=true
```

### Running the Server

1.  **Ensure your DNS is configured:**
    For local testing, you can add an entry to your `/etc/hosts` file:
    ```bash
    echo "127.0.0.1 *.tunnelfy.test" | sudo tee -a /etc/hosts
    ```
    For production, you need a wildcard DNS record (`*`) pointing to the server where Tunnelfy is running for your `ZONE`.

2.  **Run Tunnelfy:**
    You can run it directly or using the `.env` file. Tunnelfy will automatically load variables from a `.env` file if present.
    ```bash
    ./tunnelfy
    ```
    The server will start, and you should see logs indicating the SSH and HTTP listeners are active.

### Exposing a Local Service

Once the server is running, users can expose their local services.

1.  **Generate SSH Keys (for testing):**
    If you don't have a key pair, generate one:
    ```bash
    ssh-keygen -t rsa -b 4096 -f ./test_key -N ""
    ```
    Add the public key (`test_key.pub`) to the `AUTHORIZED_KEYS` variable in your `.env` file.

2.  **Start a local service:**
    For example, a simple Python HTTP server:
    ```bash
    python3 -m http.server 3000
    ```

3.  **Establish an SSH connection with a remote port forward:**
    The command forwards a remote port on the server to a local port on your machine. Tunnelfy will automatically use the SSH username to create the subdomain.

    **Command:**
    ```bash
    ssh -N -R 0:localhost:3000 -p 2222 -i ./test_key testuser@localhost
    ```
    -   `-N`: Do not execute a remote command. We only want port forwarding.
    -   `-R 0:localhost:3000`: Requests a remote port forward. The `0` tells the server to allocate a random available port, which Tunnelfy will then associate with the `testuser`. `localhost:3000` is the local service you want to expose.
    -   `-p 2222`: The port Tunnelfy's SSH server is listening on.
    -   `-i ./test_key`: The private key to use for authentication.
    -   `testuser@localhost`: Your SSH username and the domain of your Tunnelfy server (use `localhost` for local testing).

4.  **Access your service:**
    Tunnelfy will make your local service available at `http://<username>.<ZONE>`. For example, if your `ZONE` is `tunnelfy.test` and your SSH username is `testuser`, your service will be accessible at `http://testuser.tunnelfy.test:8000`.

    You can test it with `curl`:
    ```bash
    curl http://testuser.tunnelfy.test:8000
    ```

### Admin API

Tunnelfy provides a simple API endpoint to inspect currently active routes.

-   **Endpoint:** `GET /api/routes`
-   **Description:** Returns a JSON object mapping hostnames to their upstream targets.

**Example Response:**
```json
{
  "testuser.tunnelfy.test": "http://127.0.0.1:35749"
}
```

## Architecture

-   **`cmd/tunnelfy/main.go`**: Entry point, wires up SSH and HTTP servers, handles configuration and graceful shutdown.
-   **`internal/app/app.go`**: Main application logic, initializes and starts the SSH and HTTP servers.
-   **`internal/config/config.go`**: Handles loading and parsing of configuration from environment variables and `.env` files.
-   **`internal/proxy/proxy.go`**: Contains the `ShardedRouteManager` for high-performance route lookups and the `FastProxyHandler` for efficiently forwarding HTTP requests.
-   **`internal/proxy/routes_api.go`**: Implements the `/api/routes` Admin API endpoint.
-   **`internal/ssh/`**: Contains all SSH-related logic:
    -   `auth.go`: Handles public key authentication.
    -   `hostkey.go`: Manages the SSH server's host key (generates one if not provided).
    -   `server.go`: Implements the SSH server, processes `tcpip-forward` and `cancel-tcpip-forward` requests, and manages the lifecycle of the TCP listeners for each tunnel.
-   **Graceful Shutdown**: The application listens for SIGINT and SIGTERM signals. Upon receiving one, it gracefully shuts down the HTTP and SSH servers, allowing existing connections to complete.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

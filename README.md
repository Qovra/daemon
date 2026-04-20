[![Release](https://github.com/Qovra/daemon/actions/workflows/release.yml/badge.svg)](https://github.com/Qovra/daemon/actions/workflows/release.yml)

# Hytale Daemon

Hytale-Daemon is a lightweight, standalone process manager written in Go. Its sole purpose is to supervise your instances of `Hytale-Proxy`, ensuring maximum uptime and preventing crashes. Furthermore, it embeds a secure HTTP JSON REST API designed specifically to connect with web-based Control Panels (e.g. built in React/Node.js).

## 🌟 Key Features

* **Auto-Resurrection:** If the proxy process unexpectedly terminates or crashes, the daemon will automatically detect the failure and cleanly reboot it without human intervention.
* **Rest API (With CORS):** Contains built-in endpoints for starting, stopping, restarting, pulling live console logs, and getting proxy status remotely. Designed inherently to work natively via frontend web-apps.
* **Bearer Authentication:** All routes are securely protected from unauthorized access applying strict `Authorization: Bearer <token>` validations.
* **In-Memory RingBuffer Capture:** It transparently pipes the proxy's `stdout` and `stderr`, saving the latest 64KB log chunk into its own minimal memory footprint, preventing heavy disk I/O while still serving log-tails flawlessly.

## 🚀 Getting Started

Ensure the `Hytale-Proxy` source folder exists side-by-side with this directory. 

### Configuration (`daemon_config.json`)

Adjust your daemon settings by tweaking the local `daemon_config.json` before starting:

```json
{
    "api_listen": ":8080",
    "api_token": "secret-token-1234",
    "proxy_binary": "../Hytale-Proxy/proxy",
    "proxy_args": [
        "-config",
        "../Hytale-Proxy/config/example.json"
    ]
}
```

### Running Locally (Mac / Linux)

To quickly compile both the proxy and daemon and execute the ecosystem combined, simply run the included bash script:

```bash
bash start_daemon.sh
```

## 🔌 API Endpoints Documentation

The daemon exposes an HTTP Server at your configured `api_listen` port (default: `8080`).

> **Security Warning:** **All endpoints** require the standard Bearer authorization header `Authorization: Bearer secret-token-1234` to successfully fulfill the requests.

### 1. Check Status

Get information about the actual and desired proxy process states alongside performance insights.

* **Endpoint:** `GET /api/status`
* **Response `200 OK`**:
```json
{
  "desired_state": "RUNNING",
  "actual_state": "RUNNING",
  "pid": 58312,
  "uptime": "25m10s"
}
```
* **Example Usage (cURL):**
```bash
curl -X GET http://127.0.0.1:8080/api/status \
  -H "Authorization: Bearer secret-token-1234"
```

### 2. Tail Console Logs

Downloads the latest real-time chunk of standard output pushed by the Hytale-Proxy binary.

* **Endpoint:** `GET /api/logs`
* **Response `200 OK`**: Returns raw, plain-text string formatting representing the captured command line console.
* **Example Usage (cURL):**
```bash
curl -X GET http://127.0.0.1:8080/api/logs \
  -H "Authorization: Bearer secret-token-1234"
```

### 3. Graceful Restart

Safely shuts down the active process using `SIGTERM` internally and sequentially starts it up again.

* **Endpoint:** `POST /api/restart`
* **Response `200 OK`**:
```json
{
  "message": "proxy restarted successfully"
}
```
* **Example Usage (cURL):**
```bash
curl -X POST http://127.0.0.1:8080/api/restart \
  -H "Authorization: Bearer secret-token-1234"
```

### 4. Halt Server

Forcefully dictates the desired state to `STOPPED` shutting down instances completely, disabling auto-resurrection.

* **Endpoint:** `POST /api/stop`
* **Response `200 OK`**:
```json
{
  "message": "proxy stopped successfully"
}
```
* **Example Usage (cURL):**
```bash
curl -X POST http://127.0.0.1:8080/api/stop \
  -H "Authorization: Bearer secret-token-1234"
```

### 5. Boot Up Server

Instructs the proxy deployment to begin and turns Auto-resurrection back strictly on. 

* **Endpoint:** `POST /api/start`
* **Response `200 OK`**:
```json
{
  "message": "proxy started successfully"
}
```
* **Example Usage (cURL):**
```bash
curl -X POST http://127.0.0.1:8080/api/start \
  -H "Authorization: Bearer secret-token-1234"
```

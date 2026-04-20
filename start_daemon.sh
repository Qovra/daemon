#!/bin/bash
# start_daemon.sh compiles the proxy and the daemon and runs the daemon.

set -e

echo "Compiling Hytale-Proxy..."
cd ../Hytale-Proxy
go build -o proxy ./cmd/proxy
cd ../Hytale-Daemon

echo "Compiling Hytale-Daemon..."
go build -o daemon main.go

echo "Starting Hytale-Daemon..."
./daemon -config daemon_config.json

#!/bin/bash

# ThinLine Radio Server Runner
# This script runs the ThinLine Radio server using Go
#
# Usage:
#   ./run-server.sh                    # Uses default config: server/thinline-radio.ini
#   ./run-server.sh /path/to/config.ini # Uses custom config file
#   ./run-server.sh --help              # Show this help message

# Get the directory where this script is located
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
SERVER_DIR="$SCRIPT_DIR/server"

# Default config file
DEFAULT_CONFIG="$SERVER_DIR/thinline-radio.ini"

# Check for help flag
if [ "$1" == "--help" ] || [ "$1" == "-h" ]; then
    echo "ThinLine Radio Server Runner"
    echo ""
    echo "Usage:"
    echo "  ./run-server.sh                    # Use default config: server/thinline-radio.ini"
    echo "  ./run-server.sh /path/to/config.ini # Use custom config file"
    echo ""
    echo "The script will:"
    echo "  1. Change to the server directory"
    echo "  2. Run the Go server with the specified config file"
    exit 0
fi

# Use provided config file or default
if [ -n "$1" ]; then
    CONFIG_FILE="$1"
else
    CONFIG_FILE="$DEFAULT_CONFIG"
fi

# Check if config file exists
if [ ! -f "$CONFIG_FILE" ]; then
    echo "Error: Config file not found at $CONFIG_FILE"
    echo ""
    echo "Please create the config file or specify a different path."
    echo "Usage: ./run-server.sh [config-file-path]"
    exit 1
fi

# Change to server directory
cd "$SERVER_DIR" || exit 1

# Determine if we should use absolute or relative path for config
# If it's an absolute path, use it directly; otherwise make it relative to server dir
if [[ "$CONFIG_FILE" == /* ]]; then
    # Absolute path
    CONFIG_ARG="$CONFIG_FILE"
else
    # Relative path - make it relative to server directory
    CONFIG_ARG="$CONFIG_FILE"
fi

# Run the server
echo "=========================================="
echo "ThinLine Radio Server"
echo "=========================================="
echo "Config file: $CONFIG_FILE"
echo "Working directory: $SERVER_DIR"
echo "=========================================="
echo ""
go run . -config "$CONFIG_ARG"

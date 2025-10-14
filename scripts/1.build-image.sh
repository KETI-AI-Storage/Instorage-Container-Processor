#!/bin/bash

registry="ketidevit2"
image_name="instorage-container-processor_arm64"
version="v0.0.1"
dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

# Build binary file
echo "Building binary..."
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -a -ldflags '-extldflags "-static"' -o "$dir/../bin/$image_name" -mod=vendor "$dir/../cmd/main.go"

if [ $? -ne 0 ]; then
    echo "Error: Failed to build binary"
    exit 1
fi

echo "Binary built successfully: $dir/../bin/$image_name"
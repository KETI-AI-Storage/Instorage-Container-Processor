#!/bin/bash

registry="ketidevit2"
image_name="instorage-container-processor"
version="v0.0.1"
dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

echo "=========================================="
echo "Building Instorage Container Processor"
echo "Registry: $registry"
echo "Image: $image_name"
echo "Version: $version"
echo "=========================================="

# Create output directory if it doesn't exist
mkdir -p "$dir/../build/_output/bin"

# Build binary file
echo "Building binary..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -ldflags '-extldflags "-static"' -o "$dir/../build/_output/bin/$image_name" -mod=vendor "$dir/../cmd/main.go"

if [ $? -ne 0 ]; then
    echo "Error: Failed to build binary"
    exit 1
fi

echo "Binary built successfully: $dir/../build/_output/bin/$image_name"

# Build Docker image (Dockerfile must be in build/)
echo "Building Docker image..."
docker build -t $image_name:$version "$dir/../build"

if [ $? -ne 0 ]; then
    echo "Error: Failed to build Docker image"
    exit 1
fi

echo "Docker image built successfully: $image_name:$version"

# Add tag
echo "Tagging image..."
docker tag $image_name:$version $registry/$image_name:$version

# Login to registry
echo "Logging in to Docker registry..."
docker login

if [ $? -ne 0 ]; then
    echo "Error: Failed to login to Docker registry"
    exit 1
fi

# Push image
echo "Pushing image to registry..."
docker push $registry/$image_name:$version

if [ $? -eq 0 ]; then
    echo "=========================================="
    echo "Build and push completed successfully!"
    echo "Image: $registry/$image_name:$version"
    echo "=========================================="
else
    echo "Error: Failed to push image to registry"
    exit 1
fi

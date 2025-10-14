#!/bin/bash

registry="ketidevit2"
image_name="instorage-container-processor_arm64"
version="v0.0.1"
container_name="instorage-container-processor"
dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

case "$1" in
    build)
        echo "Building Docker image..."

        # Build Docker image
        docker build -t $image_name:$version "$dir/../bin"
        
        if [ $? -ne 0 ]; then
            echo "Error: Failed to build Docker image"
            exit 1
        fi
        
        echo "Docker image built successfully: $image_name:$version"
        
        # Tag image
        docker tag $image_name:$version $registry/$image_name:$version
        
        echo "Image tagged: $registry/$image_name:$version"
        ;;
        
    run)
        echo "Running container..."
        
        # 이미 실행 중인 컨테이너가 있으면 중지 및 제거
        if [ "$(docker ps -aq -f name=$container_name)" ]; then
            echo "Stopping and removing existing container..."
            docker stop $container_name 2>/dev/null
            docker rm $container_name 2>/dev/null
        fi
        
        # 컨테이너 실행
        docker run -d --name $container_name -p 40120:40120 $image_name:$version
        
        if [ $? -ne 0 ]; then
            echo "Error: Failed to run container"
            exit 1
        fi
        
        echo "Container started successfully: $container_name"
        echo "Logs: docker logs -f $container_name"
        ;;
        
    remove)
        echo "Removing container..."
        
        # 컨테이너 중지 및 제거
        if [ "$(docker ps -aq -f name=$container_name)" ]; then
            docker stop $container_name 2>/dev/null
            docker rm $container_name 2>/dev/null
            echo "Container removed: $container_name"
        else
            echo "No container found: $container_name"
        fi
        ;;

    log)
        if [ "$(docker ps -q -f name=$container_name)" ]; then
            echo "Showing logs for $container_name (Ctrl+C to exit)..."
            docker logs -f $container_name
        else
            echo "Container is not running: $container_name"
            echo "Use './2.container-processing.sh run' to start the container"
        fi
        ;;
        
    *)
        echo "Usage: $0 {build|run|remove}"
        echo "  build       - Build Docker image for AMD64"
        echo "  build cross - Build Docker image for ARM64"
        echo "  run         - Run container"
        echo "  remove      - Stop and remove container"
        exit 1
        ;;
esac
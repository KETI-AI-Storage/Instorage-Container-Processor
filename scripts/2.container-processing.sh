#!/bin/bash

# Configuration
CONTAINER_NAME="instorage-container-processor"
IMAGE_NAME="ketidevit2/instorage-container-processor:v0.0.1"
HTTP_PORT=40120

# Environment variables
MANAGER_URL="${MANAGER_URL:-http://localhost:8080}"
OPERATOR_URL="${OPERATOR_URL:-}"

# Docker socket - required for container processor to manage containers
DOCKER_SOCKET="/var/run/docker.sock"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored messages
print_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to check if container exists
container_exists() {
    docker ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"
}

# Function to check if container is running
container_running() {
    docker ps --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"
}

# Function to start container
start_container() {
    print_info "Starting container: $CONTAINER_NAME"

    if container_running; then
        print_warning "Container is already running"
        return 0
    fi

    if container_exists; then
        docker start "$CONTAINER_NAME"
        if [ $? -eq 0 ]; then
            print_info "Container started successfully"
            print_info "HTTP Port: $HTTP_PORT"
        else
            print_error "Failed to start container"
            return 1
        fi
    else
        print_error "Container does not exist. Use 'run' command first."
        return 1
    fi
}

# Function to stop container
stop_container() {
    print_info "Stopping container: $CONTAINER_NAME"

    if ! container_running; then
        print_warning "Container is not running"
        return 0
    fi

    docker stop "$CONTAINER_NAME"
    if [ $? -eq 0 ]; then
        print_info "Container stopped successfully"
    else
        print_error "Failed to stop container"
        return 1
    fi
}

# Function to remove container
remove_container() {
    print_info "Removing container: $CONTAINER_NAME"

    if container_running; then
        print_warning "Container is running. Stopping first..."
        stop_container
    fi

    if container_exists; then
        docker rm "$CONTAINER_NAME"
        if [ $? -eq 0 ]; then
            print_info "Container removed successfully"
        else
            print_error "Failed to remove container"
            return 1
        fi
    else
        print_warning "Container does not exist"
    fi
}

# Function to run container
run_container() {
    print_info "Running container: $CONTAINER_NAME"

    if container_exists; then
        print_error "Container already exists. Use 'start' to start it or 'remove' to delete it first."
        return 1
    fi

    # Check if Docker socket exists
    if [ ! -S "$DOCKER_SOCKET" ]; then
        print_error "Docker socket not found at $DOCKER_SOCKET"
        return 1
    fi

    print_info "Configuration:"
    print_info "  Image: $IMAGE_NAME"
    print_info "  HTTP Port: $HTTP_PORT"
    print_info "  Manager URL: $MANAGER_URL"
    print_info "  Operator URL: $OPERATOR_URL"
    print_info "  Docker Socket: $DOCKER_SOCKET"

    docker run -d \
        --name "$CONTAINER_NAME" \
        --network host \
        -p ${HTTP_PORT}:${HTTP_PORT} \
        -e MANAGER_URL="$MANAGER_URL" \
        -e OPERATOR_URL="$OPERATOR_URL" \
        -v ${DOCKER_SOCKET}:/var/run/docker.sock \
        "$IMAGE_NAME" \
        --http-port=${HTTP_PORT}

    if [ $? -eq 0 ]; then
        print_info "Container started successfully"
        print_info "Container name: $CONTAINER_NAME"
        print_info "HTTP endpoint: http://localhost:${HTTP_PORT}"
        print_info ""
        print_info "Use 'docker logs -f $CONTAINER_NAME' to view logs"
    else
        print_error "Failed to start container"
        return 1
    fi
}

# Function to show container status
status_container() {
    print_info "Checking container status: $CONTAINER_NAME"

    if container_running; then
        print_info "Container is running"
        docker ps --filter "name=$CONTAINER_NAME" --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"
        echo ""
        print_info "Recent logs:"
        docker logs --tail 20 "$CONTAINER_NAME"
    elif container_exists; then
        print_warning "Container exists but is not running"
        docker ps -a --filter "name=$CONTAINER_NAME" --format "table {{.Names}}\t{{.Status}}"
    else
        print_warning "Container does not exist"
    fi
}

# Function to show logs
logs_container() {
    print_info "Showing logs for container: $CONTAINER_NAME"

    if ! container_exists; then
        print_error "Container does not exist"
        return 1
    fi

    docker logs -f "$CONTAINER_NAME"
}

# Function to restart container
restart_container() {
    print_info "Restarting container: $CONTAINER_NAME"

    if container_running; then
        stop_container
        sleep 2
    fi

    start_container
}

# Function to show usage
usage() {
    cat << EOF
Usage: $0 <command>

Commands:
    run        Create and start a new container
    start      Start an existing container
    stop       Stop a running container
    restart    Restart the container
    remove     Remove the container (stops it first if running)
    status     Show container status and recent logs
    logs       Follow container logs in real-time
    help       Show this help message

Environment Variables:
    MANAGER_URL    - URL of instorage-manager (default: http://localhost:8080)
    OPERATOR_URL   - URL of instorage-operator (default: empty)

Examples:
    # Run container with default settings
    $0 run

    # Run container with custom manager URL
    MANAGER_URL=http://10.1.1.2:8080 $0 run

    # Stop container
    $0 stop

    # View logs
    $0 logs

    # Check status
    $0 status

    # Remove container
    $0 remove

EOF
}

# Main script
main() {
    if [ $# -eq 0 ]; then
        print_error "No command specified"
        usage
        exit 1
    fi

    command=$1

    case "$command" in
        run)
            run_container
            ;;
        start)
            start_container
            ;;
        stop)
            stop_container
            ;;
        restart)
            restart_container
            ;;
        remove|rm)
            remove_container
            ;;
        status)
            status_container
            ;;
        logs)
            logs_container
            ;;
        help|--help|-h)
            usage
            ;;
        *)
            print_error "Unknown command: $command"
            usage
            exit 1
            ;;
    esac
}

main "$@"

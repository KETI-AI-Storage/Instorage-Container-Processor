FROM golang:1.21-alpine

# Install ca-certificates for HTTPS and Docker socket access
RUN apk --no-cache add ca-certificates

ENV PROCESSOR=/usr/local/bin/instorage-container-processor \
    USER_UID=1001 \
    USER_NAME=processor

# Create non-root user
RUN adduser -D -u ${USER_UID} ${USER_NAME}

# Create required directories
RUN mkdir -p /home/${USER_NAME} && \
    chown -R ${USER_UID}:${USER_UID} /home/${USER_NAME}

# Copy the processor binary (built by build script)
COPY instorage-container-processor_arm64 ${PROCESSOR}

# Make binary executable
RUN chmod +x ${PROCESSOR}

# Switch to non-root user
USER ${USER_UID}

# Expose HTTP port
EXPOSE 40120

ENTRYPOINT ["/usr/local/bin/instorage-container-processor"]

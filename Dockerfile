# Use a slim, modern Python image.
# Pinned to the linux/amd64 manifest digest for hermetic, reproducible builds.
# To re-pin: docker manifest inspect python:3.11-slim | jq '.manifests[] | select(.platform.os=="linux" and .platform.architecture=="amd64") | .digest'
FROM python:3.11-slim@sha256:506f2951c04925a2a98fc0604e0e0a3ffd2788cc4aed0eb77c3517c98ac651c0

# Set a working directory
WORKDIR /app

# Install system dependencies that might be needed for cryptographic libraries
# and pip-tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    libffi-dev \
    git \
    curl \
    jq \
    && rm -rf /var/lib/apt/lists/*

# Install pip-tools globally in the container for the setup service to use
RUN pip install --no-cache-dir pip-tools

# Use a slim, modern Python image.
# Pinned to the linux/amd64 manifest digest for hermetic, reproducible builds.
# To re-pin: docker manifest inspect python:3.11-slim | jq '.manifests[] | select(.platform.os=="linux" and .platform.architecture=="amd64") | .digest'
FROM python:3.11-slim@sha256:272efc3f94bec901e2bc7a5c9984b3d113c317839edca2714c6a388b9476f8fc

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

# Stage 1: Builder
# Use the official Golang image to build the application.
FROM golang:1.21-alpine AS builder

# Set the working directory inside the container.
WORKDIR /app

# Copy go mod and sum files to download dependencies.
COPY go.mod go.sum ./

# Download dependencies. This is a separate step to leverage Docker layer caching.
RUN go mod download

# Copy the source code into the container.
COPY . .

# Build the application.
# -w -s flags reduce the binary size by removing debug information.
# -o tunnelfy specifies the output binary name.
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags '-w -s' -o tunnelfy ./cmd/tunnelfy

# Stage 2: Runner
# Use a minimal Alpine image.
FROM alpine:latest

# Install ca-certificates for HTTPS requests and bash for potential debugging.
RUN apk --no-cache add ca-certificates bash

# Create a non-root user to run the application for security.
RUN addgroup -g 1000 -S appuser && \
    adduser -u 1000 -S appuser -G appuser

# Set the working directory.
WORKDIR /root/

# Copy the binary from the builder stage.
COPY --from=builder /app/tunnelfy .

# Copy the .env.example file (if it exists) to provide a template for users.
# The actual .env file should be mounted as a volume in production.
COPY .env.example .env.example  # This line is optional, remove if .env.example doesn't exist

# Switch to the non-root user.
USER appuser

# Expose the ports the application uses.
# 2222 for SSH, 8000 for HTTP.
EXPOSE 2222 8000

# The command to run the application.
CMD ["./tunnelfy"]

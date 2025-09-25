FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Build the application.
# -w -s flags reduce the binary size by removing debug information.
# -o tunnelfy specifies the output binary name.
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags '-w -s' -o tunnelfy ./cmd/tunnelfy

# Stage 2: Runner
FROM alpine:latest

RUN apk --no-cache add ca-certificates bash
RUN addgroup -g 1000 -S appuser && \
    adduser -u 1000 -S appuser -G appuser
WORKDIR /root/

COPY --from=builder /app/tunnelfy .
USER appuser

EXPOSE 2222 8000
CMD ["./tunnelfy"]

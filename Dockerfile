# Builder
FROM rust:1.77 as builder
WORKDIR /app
COPY . .
RUN cargo build --release

# Runtime
FROM debian:bullseye-slim
WORKDIR /app
COPY --from=builder /app/target/release/tunnelfy /app/tunnelfy
COPY --from=builder /app/tunnelfy.db ./
ENV STATIC_PROXIES=portal.example.com:8081,admin.example.com:8082
EXPOSE 8080
CMD ["./tunnelfy"]

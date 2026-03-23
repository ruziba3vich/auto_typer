FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod main.go ./
RUN go build -o /auto_typer .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ydotool xclip wl-clipboard \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /auto_typer /usr/local/bin/auto_typer
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]

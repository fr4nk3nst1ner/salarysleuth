# Build and runtime stage - use same image to avoid network issues
FROM golang:1.23-bookworm

WORKDIR /build

# Copy the main salarysleuth project
COPY . .

# Build the main salarysleuth executable
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/salarysleuth ./cmd/salarysleuth/main.go && chmod +x /app/salarysleuth

# Build the jobtracker binary
WORKDIR /build/jobtracker
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -a -installsuffix cgo -o /app/jobtracker . && chmod +x /app/jobtracker

# Switch to app directory
WORKDIR /app

# Create data directory
RUN mkdir -p /app/data

# Copy entrypoint script (it's already in /build from the COPY . . earlier)
RUN cp /build/jobtracker/docker-entrypoint.sh /app/entrypoint.sh && chmod +x /app/entrypoint.sh

# Environment variables
ENV TELEGRAM_BOT_TOKEN=""
ENV TELEGRAM_CHAT_ID=""
ENV WEB_PORT=8080
ENV SCRAPE_PAGES=20
ENV SCRAPE_DESCRIPTION="Offensive Security"
ENV TZ=America/Chicago
ENV DATA_DIR=/app/data

# Expose web port
EXPOSE 8080

# Data volume
VOLUME ["/app/data"]

ENTRYPOINT ["/app/entrypoint.sh"]

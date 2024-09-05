# Use the official Go image as the base image
FROM golang:1.21

# Set the working directory
WORKDIR /app

# Copy the Go application files to the working directory
COPY . .

# Download necessary Go modules (if using go.mod and go.sum)
RUN go mod tidy
RUN go mod download

# Build the Go application
RUN go build -o salarysleuth .

# Set the entry point to run the Go application with arguments
ENTRYPOINT ["./salarysleuth"]


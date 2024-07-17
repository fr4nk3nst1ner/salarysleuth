# Use the official Go image as the base image
FROM golang:1.20-buster

# Set the working directory
WORKDIR /app

# Copy the Go application files to the working directory
COPY salarysleuth.go ./

# Install go-dork
RUN go install dw1.io/go-dork@latest

# Expose the port
EXPOSE 80

# Build the Go application
RUN go build -o salarysleuth .

# Set the entry point to run the Go application with arguments
ENTRYPOINT ["./salarysleuth"]


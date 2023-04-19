# Specify the base image
FROM python:3.8-slim-buster

# Set the working directory
WORKDIR /app

# Copy the application files to the working directory
COPY salarysleuth.py requirements.txt ./

# Install the required packages
RUN pip install --no-cache-dir -r requirements.txt

# install go
RUN apt update ; apt install wget -y ; wget https://go.dev/dl/go1.20.2.linux-amd64.tar.gz -O /tmp/go.tar.gz ; tar -C /usr/local -xzf /tmp/go.tar.gz

# install go dork
RUN export PATH=$PATH:/usr/local/go/bin ;  echo "export PATH=$PATH:/usr/local/go/bin" >> ~/.bashrc ; source ~/.bashrc ; GO111MODULE=on go install dw1.io/go-dork@latest

# set env path
ENV PATH="${PATH}:/root/go/bin:/app"

# Expose the port to run the application
EXPOSE 80

# Run the application
CMD ["python", "/app/salarysleuth.py"]

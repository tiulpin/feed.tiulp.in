FROM glanceapp/glance:latest

# Copy configuration files
COPY config/ /app/config/
COPY assets/ /app/assets/

# Set timezone
ENV TZ=Europe/Amsterdam

EXPOSE 8080


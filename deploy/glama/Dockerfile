# The image Glama builds for filex (deploy/glama/README.md), to try a change
# here before it is pushed. Glama generates its own Dockerfile from the
# settings in the README: this is that file with the repository copied in
# instead of cloned, and without the Python toolchain filex does not use.
#
#   docker build -f deploy/glama/Dockerfile -t filex-glama .
FROM debian:trixie-slim

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
 && curl -fsSL https://deb.nodesource.com/setup_24.x | bash - \
 && apt-get install -y --no-install-recommends nodejs \
 && npm install -g mcp-proxy@6.9.0 \
 && apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY web/package.json web/package.json
COPY deploy/glama deploy/glama
RUN sh deploy/glama/build.sh

CMD ["mcp-proxy", "--", "/usr/local/bin/filex-mcp-stdio"]

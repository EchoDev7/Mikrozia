#!/bin/bash

# HOME=/root is baked into the image; keep this entrypoint free of setup writes.
if [ $# -gt 0 ] && [ "$1" != "./x-ui" ]; then
    exec "$@"
else
    exec /app/x-ui
fi

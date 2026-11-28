# The manager's image for make e2e, from the binary under test (the build context is bin/): the analytics server and
# the volume helpers run from it. The published image's last stage (../../Dockerfile), without rebuilding the binary.
FROM gcr.io/distroless/static-debian12
COPY kipitiny /kipitiny
# Owned by nonroot for `kipitiny analytics`, which runs as 65532 with its own volume here.
COPY --from=gcr.io/distroless/static-debian12 --chown=65532:65532 /tmp /data
ENV KIPITINY_DATA_DIR=/data
ENTRYPOINT ["/kipitiny"]

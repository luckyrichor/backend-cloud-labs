FROM busybox:1.37.0@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e
ARG SOURCE_COMMIT=unverified
LABEL org.opencontainers.image.revision=$SOURCE_COMMIT
COPY .local/bin/e1 /service
RUN mkdir /data && chown 65532:65532 /data
USER 65532:65532
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s CMD ["/service", "healthcheck"]
ENTRYPOINT ["/service"]

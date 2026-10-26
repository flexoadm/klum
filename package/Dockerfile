FROM scratch
ARG TARGETARCH
COPY --chmod=0755 dist/artifacts/klum-${TARGETARCH} /klum
CMD ["/klum"]
